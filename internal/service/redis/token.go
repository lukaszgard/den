package redis

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/lukaszgard/den/internal/service/awsauth"
)

const (
	tokenTTL     = 15 * time.Minute
	redisAWSPort = 6379

	signingElastiCache = "elasticache"
	signingMemoryDB    = "memorydb"
)

// tokenTarget names what an IAM auth token is minted for. ElastiCache and
// MemoryDB use the same presigned-URL scheme and differ only in the SigV4
// signing name and ElastiCache Serverless's extra ResourceType parameter.
type tokenTarget struct {
	name       string // replication group ID, serverless cache name or MemoryDB cluster name
	userID     string
	region     string
	service    string // SigV4 signing name: signingElastiCache or signingMemoryDB
	serverless bool   // ElastiCache Serverless only
}

// generateToken generates an IAM authentication token for ElastiCache or MemoryDB.
// Follows the canonical pattern from build-on-aws/aws-redis-iam-auth-golang:
// use v4.PresignHTTP so the SDK handles SigV4 correctly instead of manual signing.
func generateToken(ctx context.Context, target tokenTarget, profile string) (string, error) {
	cfg, err := awsauth.LoadConfig(ctx, profile, target.region)
	if err != nil {
		return "", err
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return "", fmt.Errorf("retrieve credentials: %w", err)
	}
	return presignToken(ctx, creds, target, time.Now().UTC())
}

// presignToken builds the token itself: a SigV4-presigned "connect" request with
// the scheme stripped, which the cache verifies as the AUTH password.
func presignToken(ctx context.Context, creds aws.Credentials, target tokenTarget, now time.Time) (string, error) {
	query := url.Values{
		"Action":        {"connect"},
		"User":          {target.userID},
		"X-Amz-Expires": {"900"},
	}
	if target.serverless {
		query.Set("ResourceType", "ServerlessCache")
	}

	endpoint := "http://" + target.name + "/?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	signer := v4.NewSigner()
	signedURL, _, err := signer.PresignHTTP(ctx, creds, req,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", // SHA-256("")
		target.service, target.region, now)
	if err != nil {
		return "", fmt.Errorf("presign: %w", err)
	}

	return strings.TrimPrefix(signedURL, "http://"), nil
}

func tokenExpiresAt() time.Time {
	return time.Now().Add(tokenTTL)
}
