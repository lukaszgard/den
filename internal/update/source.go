package update

import (
	"os"

	"github.com/lukaszgard/den/internal/config"
)

// NewSource builds the release source from den.yaml's updates block and the
// environment, the same for the Updates pane and `den update`.
func NewSource(cfg *config.UpdatesConfig) Source {
	src := Source{Token: os.Getenv("GITHUB_TOKEN")}
	if cfg != nil {
		src.GitHubAPI = cfg.GitHubAPI
		src.S3Bucket = cfg.S3Bucket
		src.S3Prefix = cfg.S3Prefix
		src.AWSProfile = cfg.AWSProfile
		src.AWSRegion = cfg.AWSRegion
	}
	return src
}
