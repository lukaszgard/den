package redshift

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	rs "github.com/aws/aws-sdk-go-v2/service/redshift"
	rss "github.com/aws/aws-sdk-go-v2/service/redshiftserverless"

	"github.com/lukaszgard/den/internal/config"
)

// tempCreds is a Redshift temporary database login. User is what Redshift
// returns, not what was asked for: it carries an "IAM:"/"IAMR:" prefix, and
// logging in under any other name fails.
type tempCreds struct {
	User     string
	Password string
	Expires  time.Time
}

// provisionedAPI and serverlessAPI are the slices of the SDK clients den uses,
// so the API choice can be tested without AWS.
type provisionedAPI interface {
	GetClusterCredentialsWithIAM(context.Context, *rs.GetClusterCredentialsWithIAMInput, ...func(*rs.Options)) (*rs.GetClusterCredentialsWithIAMOutput, error)
	GetClusterCredentials(context.Context, *rs.GetClusterCredentialsInput, ...func(*rs.Options)) (*rs.GetClusterCredentialsOutput, error)
}

type serverlessAPI interface {
	GetCredentials(context.Context, *rss.GetCredentialsInput, ...func(*rss.Options)) (*rss.GetCredentialsOutput, error)
}

// fetchCredentials mints temporary credentials through whichever API the spec
// selects:
//   - Serverless (workgroup_name): GetCredentials, mapped to the caller's IAM identity.
//   - Provisioned without db_user: GetClusterCredentialsWithIAM, likewise.
//   - Provisioned with db_user: GetClusterCredentials for that existing user
//     (AutoCreate off — den should never create database users as a side effect).
func fetchCredentials(ctx context.Context, spec *config.RedshiftSpec, ttl time.Duration,
	prov provisionedAPI, sls serverlessAPI) (tempCreds, error) {
	seconds := aws.Int32(int32(ttl / time.Second)) //nolint:gosec // ttl is validated to 15m–1h (config.validateRedshift)
	dbName := aws.String(spec.DBName)

	var (
		user, password *string
		expires        *time.Time
	)
	switch {
	case spec.WorkgroupName != "":
		out, err := sls.GetCredentials(ctx, &rss.GetCredentialsInput{
			WorkgroupName:   aws.String(spec.WorkgroupName),
			DbName:          dbName,
			DurationSeconds: seconds,
		})
		if err != nil {
			return tempCreds{}, fmt.Errorf("redshift-serverless GetCredentials: %w", err)
		}
		user, password, expires = out.DbUser, out.DbPassword, out.Expiration
	case spec.DBUser == "":
		out, err := prov.GetClusterCredentialsWithIAM(ctx, &rs.GetClusterCredentialsWithIAMInput{
			ClusterIdentifier: aws.String(spec.ClusterIdentifier),
			DbName:            dbName,
			DurationSeconds:   seconds,
		})
		if err != nil {
			return tempCreds{}, fmt.Errorf("redshift GetClusterCredentialsWithIAM: %w", err)
		}
		user, password, expires = out.DbUser, out.DbPassword, out.Expiration
	default:
		out, err := prov.GetClusterCredentials(ctx, &rs.GetClusterCredentialsInput{
			ClusterIdentifier: aws.String(spec.ClusterIdentifier),
			DbUser:            aws.String(spec.DBUser),
			DbName:            dbName,
			DurationSeconds:   seconds,
			AutoCreate:        aws.Bool(false),
		})
		if err != nil {
			return tempCreds{}, fmt.Errorf("redshift GetClusterCredentials: %w", err)
		}
		user, password, expires = out.DbUser, out.DbPassword, out.Expiration
	}

	c := tempCreds{User: aws.ToString(user), Password: aws.ToString(password)}
	if c.User == "" || c.Password == "" {
		return tempCreds{}, fmt.Errorf("redshift returned no credentials")
	}
	if expires != nil {
		c.Expires = *expires
	} else {
		c.Expires = time.Now().Add(ttl)
	}
	return c, nil
}
