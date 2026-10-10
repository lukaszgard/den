// Package awssm reads AWS Secrets Manager secrets with the AWS SDK, as the
// configured profile. It is read-only: den never writes a secret back.
package awssm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
	"github.com/lukaszgard/den/internal/service/awsauth"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
)

// readTimeout bounds a read, SSO credential exchange included.
const readTimeout = 30 * time.Second

// maxErrLen caps the error shown in the pane.
const maxErrLen = 600 // IAM denials put the reason ("because …") at the end

// getter is the part of the Secrets Manager client den uses, so tests can
// stand in for AWS.
type getter interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// Item is one Secrets Manager secret.
type Item struct {
	spec      config.AWSSecretsManagerSecretSpec
	aws       *secrets.AWSLogins // checks aws_profile; nil skips the check
	newClient func(ctx context.Context) (getter, error)
}

var (
	_ secrets.Item    = (*Item)(nil)
	_ secrets.Viewer  = (*Item)(nil)
	_ secrets.AWSUser = (*Item)(nil)
)

// New builds an item from its config entry. aws is the config's shared view
// of the AWS logins, used to check aws_profile; nil skips that check.
func New(spec config.AWSSecretsManagerSecretSpec, aws *secrets.AWSLogins) *Item {
	it := &Item{spec: spec, aws: aws}
	it.newClient = func(ctx context.Context) (getter, error) {
		cfg, err := awsauth.LoadConfig(ctx, spec.AWSProfile, spec.AWSRegion)
		if err != nil {
			return nil, err
		}
		return secretsmanager.NewFromConfig(cfg), nil
	}
	return it
}

func (it *Item) Name() string        { return it.spec.Name }
func (it *Item) Description() string { return it.spec.Description }
func (it *Item) Provider() string    { return "aws secrets manager" }
func (it *Item) Location() string    { return it.spec.SecretID }
func (it *Item) AutoReveal() bool    { return it.spec.AutoReveal }

// TreeView reports whether the values open as a tree. providers.Items has
// already filled an empty view from secrets.view, so empty here means tree.
func (it *Item) TreeView() bool { return it.spec.View != config.SecretViewFlat }

func (it *Item) Details() []secrets.Detail {
	profile := it.spec.AWSProfile
	if profile == "" {
		profile = "(inherited from the environment)"
	}
	d := []secrets.Detail{
		{Label: "Secret", Value: it.spec.SecretID},
		{Label: "AWS profile", Value: profile},
	}
	if it.spec.AWSRegion != "" {
		d = append(d, secrets.Detail{Label: "AWS region", Value: it.spec.AWSRegion})
	}
	if it.spec.VersionStage != "" {
		d = append(d, secrets.Detail{Label: "Version", Value: it.spec.VersionStage})
	}
	return d
}

// Probe checks aws_profile without calling AWS; everything else (permissions,
// a wrong secret_id) only shows up on Read.
func (it *Item) Probe() secrets.Status {
	if reason := it.aws.Check(it.spec.AWSProfile, time.Now()); reason != "" {
		return secrets.Status{Reason: reason}
	}
	return secrets.Status{Ready: true}
}

// UsesAWS is always true: every read goes through aws_profile's login.
func (it *Item) UsesAWS() bool { return true }

// Read fetches the secret. A JSON object or array is flattened like a sops
// file; anything else is a single "value" row.
func (it *Item) Read(ctx context.Context) ([]secrets.Field, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	client, err := it.newClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("secrets manager: %s", oneLine(err.Error()))
	}
	in := &secretsmanager.GetSecretValueInput{SecretId: aws.String(it.spec.SecretID)}
	if it.spec.VersionStage != "" {
		in.VersionStage = aws.String(it.spec.VersionStage)
	}
	out, err := client.GetSecretValue(ctx, in)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("secrets manager: timed out after %s", readTimeout)
		}
		return nil, fmt.Errorf("secrets manager: %s", describe(err))
	}

	if out.SecretString != nil {
		return parse(*out.SecretString), nil
	}
	if out.SecretBinary != nil {
		b := out.SecretBinary
		defer clear(b)
		return []secrets.Field{{Key: "value (base64)", Value: base64.StdEncoding.EncodeToString(b)}}, nil
	}
	return nil, nil
}

// parse flattens a JSON secret. A string that only looks like JSON (a
// password starting with "{", say) falls back to one row, as it is.
func parse(s string) []secrets.Field {
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		if fields, err := secrets.FlattenJSON([]byte(t)); err == nil {
			return fields
		}
	}
	return []secrets.Field{{Key: "value", Value: s}}
}

// describe turns an SDK error into "Code: message", dropping the request IDs
// and retry chatter that make SDK errors long.
func describe(err error) string {
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		return oneLine(apiErr.ErrorCode() + ": " + apiErr.ErrorMessage())
	}
	return oneLine(err.Error())
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxErrLen {
		s = string(r[:maxErrLen-1]) + "…"
	}
	return s
}
