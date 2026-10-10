package rds

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	tokenTTL        = 15 * time.Minute
	postgresAWSPort = 5432
	mysqlAWSPort    = 3306
)

// tokenArgs builds the aws CLI arguments that mint the token. An empty profile
// or region is left out: an empty flag would make the CLI fail on "", where
// leaving it out lets AWS_PROFILE, AWS_REGION and the default chain decide.
func tokenArgs(rdsHost string, port int, regionCode, profile, dbUser string) []string {
	args := []string{"rds", "generate-db-auth-token", "--hostname", rdsHost, "--port", strconv.Itoa(port)}
	if regionCode != "" {
		args = append(args, "--region", regionCode)
	}
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	return append(args, "--username", dbUser)
}

// generateToken calls the AWS CLI to produce an IAM auth token.
func generateToken(ctx context.Context, rdsHost string, port int, regionCode, profile, dbUser string) (string, error) {
	cmd := exec.CommandContext(ctx, "aws", tokenArgs(rdsHost, port, regionCode, profile, dbUser)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("generate-db-auth-token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("generate-db-auth-token: empty output")
	}
	return token, nil
}

// tokenExpiresAt returns the expected expiry time for a token generated now.
func tokenExpiresAt() time.Time {
	return time.Now().Add(tokenTTL)
}
