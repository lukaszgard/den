package documentdb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/platform"
)

// caBundleFor is where the RDS global CA bundle for a region's partition is
// published, and the file den caches it in. The commercial and China
// partitions have disjoint CAs, so a bundle cached for one must never be
// handed to the other: mongosh then fails with "unable to get local issuer
// certificate" over a tunnel that works. China publishes its bundles in an S3
// bucket (docs.amazonaws.cn), not under a truststore.pki host; its global
// bundle holds the CAs of every China region, so one file serves them all.
func caBundleFor(regionCode string) (url, file string) {
	if strings.HasPrefix(regionCode, "cn-") {
		return "https://rds-truststore.s3.cn-north-1.amazonaws.com.cn/global/global-bundle.pem", "global-bundle-cn.pem"
	}
	return "https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem", "global-bundle.pem"
}

// resolveCABundle returns a local path to the Amazon RDS global CA bundle that
// mongosh needs for the TLS handshake (DocumentDB requires TLS).
//
// An explicitly configured path is used as-is and must exist — den will not
// silently substitute a downloaded bundle for one the user named. Otherwise the
// bundle of the region's partition is cached in the config directory
// (caBundleFor names the file) and downloaded on first use.
func resolveCABundle(ctx context.Context, regionCode, configured string, logf func(string)) (string, error) {
	if configured != "" {
		path := platform.ExpandHome(configured)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("ca_file %s: %w", path, err)
		}
		return path, nil
	}

	path := defaultCABundlePath(regionCode)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	url, _ := caBundleFor(regionCode)
	logf(fmt.Sprintf("CA bundle not found — downloading %s", url))

	if err := downloadTo(ctx, url, path); err != nil {
		return "", fmt.Errorf("download CA bundle: %w (fetch it manually with: curl -o %s %s)", err, path, url)
	}
	logf(fmt.Sprintf("CA bundle saved to %s", path))
	return path, nil
}

// defaultCABundlePath is where den caches the RDS global CA bundle for the
// region's partition.
func defaultCABundlePath(regionCode string) string {
	_, file := caBundleFor(regionCode)
	dir := platform.ConfigDir()
	if dir == "" {
		return file
	}
	return filepath.Join(dir, file)
}

// downloadTo fetches url and writes it to path, creating parent directories.
// It writes to a temp file first so an interrupted download cannot leave a
// truncated bundle behind that would then be treated as cached on the next run.
func downloadTo(ctx context.Context, url, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".global-bundle-*.pem")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck — no-op once the rename succeeds

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
