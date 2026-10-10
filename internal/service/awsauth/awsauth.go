// Package awsauth loads the AWS SDK configuration services use to mint
// credentials in-process, as the named profile in the given region.
package awsauth

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// LoadConfig loads the shared AWS config for profile (the default chain when
// empty). Region must be explicit so the SDK uses the correct regional STS
// endpoint when exchanging an SSO token for temporary credentials.
func LoadConfig(ctx context.Context, profile, region string) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}
	return cfg, nil
}
