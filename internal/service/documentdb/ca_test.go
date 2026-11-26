package documentdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The CA bundle is cached beside den.yaml, so XDG_CONFIG_HOME moves both; a
// China region gets a file of its own.
func TestDefaultCABundlePathFollowsConfigDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	for region, file := range map[string]string{
		"eu-central-1":   "global-bundle.pem",
		"us-east-1":      "global-bundle.pem",
		"":               "global-bundle.pem",
		"cn-north-1":     "global-bundle-cn.pem",
		"cn-northwest-1": "global-bundle-cn.pem",
	} {
		if got, want := defaultCABundlePath(region), filepath.Join(xdg, "den", file); got != want {
			t.Errorf("defaultCABundlePath(%q) = %q, want %q", region, got, want)
		}
	}
}

// The China partition's CAs are published in an S3 bucket in cn-north-1, not
// under truststore.pki.rds.amazonaws.com's naming (that China host does not
// resolve). Its global bundle holds the CAs of every China region.
func TestCABundleForChinaIsTheDocumentedURL(t *testing.T) {
	for _, region := range []string{"cn-north-1", "cn-northwest-1"} {
		url, file := caBundleFor(region)
		if want := "https://rds-truststore.s3.cn-north-1.amazonaws.com.cn/global/global-bundle.pem"; url != want {
			t.Errorf("caBundleFor(%q) url = %q, want %q", region, url, want)
		}
		if file != "global-bundle-cn.pem" {
			t.Errorf("caBundleFor(%q) file = %q, want global-bundle-cn.pem", region, file)
		}
	}
	url, file := caBundleFor("eu-central-1")
	if url != "https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem" || file != "global-bundle.pem" {
		t.Errorf("caBundleFor(eu-central-1) = %q, %q", url, file)
	}
}

// A bundle cached for one partition must never be handed to a cluster in the
// other: their CAs are disjoint, and mongosh then fails with "unable to get
// local issuer certificate" although the tunnel is fine.
func TestResolveCABundleKeepsPartitionsApart(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "den")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"global-bundle.pem", "global-bundle-cn.pem"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("cached\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	noLog := func(msg string) { t.Errorf("a cached bundle was downloaded again: %s", msg) }

	for region, file := range map[string]string{
		"cn-north-1":     "global-bundle-cn.pem",
		"cn-northwest-1": "global-bundle-cn.pem",
		"eu-central-1":   "global-bundle.pem",
	} {
		got, err := resolveCABundle(context.Background(), region, "", noLog)
		if err != nil {
			t.Fatalf("resolveCABundle(%q): %v", region, err)
		}
		if want := filepath.Join(dir, file); got != want {
			t.Errorf("resolveCABundle(%q) = %q, want %q", region, got, want)
		}
	}
}
