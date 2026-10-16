//go:build !windows

package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeAWSScript maps s3://<bucket>/<key> onto $DEN_FAKE_S3/<bucket>/<key> for
// the three calls the updater makes: s3 cp to stdout, s3 cp to a file, and
// s3api head-object for the size. A missing object fails the way the CLI does.
const fakeAWSScript = `#!/bin/sh
root="$DEN_FAKE_S3"
if [ "$DEN_FAKE_S3_EXPIRED" = 1 ]; then
	echo "Error loading SSO Token: Token for x does not exist" >&2
	exit 255
fi
case "$1 $2" in
"s3 cp")
	src="$root/${3#s3://}"
	[ -f "$src" ] || { echo "fatal error: An error occurred (404) when calling the HeadObject operation" >&2; exit 1; }
	if [ "$4" = "-" ]; then cat "$src"; else cp "$src" "$4"; fi
	;;
"s3api head-object")
	shift 2
	while [ $# -gt 0 ]; do
		case "$1" in
		--bucket) bucket="$2"; shift ;;
		--key) key="$2"; shift ;;
		esac
		shift
	done
	obj="$root/$bucket/$key"
	[ -f "$obj" ] || { echo "An error occurred (404) when calling the HeadObject operation: Not Found" >&2; exit 254; }
	wc -c < "$obj" | tr -d ' '
	;;
*)
	echo "unexpected: $*" >&2
	exit 2
	;;
esac
`

func fakeS3(t *testing.T) string {
	t.Helper()
	bin, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(fakeAWSScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEN_FAKE_S3", root)
	return root
}

func TestS3DownloadVerified(t *testing.T) {
	root := fakeS3(t)
	archive := ArchiveName("v2.0.0", runtime.GOOS, runtime.GOARCH)
	data := tarGz(t, map[string]string{"den": "binary"})
	writeFile(t, filepath.Join(root, "bucket", "den", "latest.json"), []byte(`{"version":"v2.0.0","notes":"n"}`))
	writeFile(t, filepath.Join(root, "bucket", "den", "v2.0.0", archive), data)
	writeFile(t, filepath.Join(root, "bucket", "den", "v2.0.0", ChecksumsFile), []byte(fmt.Sprintf("%s  %s\n", sha(data), archive)))

	src := Source{S3Bucket: "bucket", S3Prefix: "den/"}
	ctx := context.Background()
	rel, err := src.Latest(ctx, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "v2.0.0" || rel.Archive != archive || rel.Notes != "n" {
		t.Fatalf("release = %+v", rel)
	}

	path := filepath.Join(t.TempDir(), "download")
	var total atomic.Int64
	if err := src.Download(ctx, rel, path, nil, &total); err != nil {
		t.Fatal(err)
	}
	if total.Load() != int64(len(data)) {
		t.Errorf("total = %d, want %d", total.Load(), len(data))
	}
	sums, err := src.Checksums(ctx, rel)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(path, rel.Archive, sums); err != nil {
		t.Fatal(err)
	}
}

func TestS3ReleaseWithoutChecksums(t *testing.T) {
	root := fakeS3(t)
	writeFile(t, filepath.Join(root, "bucket", "latest.json"), []byte(`{"version":"v2.0.0"}`))
	src := Source{S3Bucket: "bucket"}
	rel, err := src.Latest(context.Background(), "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Checksums(context.Background(), rel); !errors.Is(err, ErrNoChecksums) {
		t.Fatalf("err = %v, want ErrNoChecksums", err)
	}
}

func TestS3ExpiredLoginNamesTheFix(t *testing.T) {
	fakeS3(t)
	t.Setenv("DEN_FAKE_S3_EXPIRED", "1")
	_, err := Source{S3Bucket: "bucket", AWSProfile: "p"}.Latest(context.Background(), "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "aws sso login --profile p") {
		t.Fatalf("err = %v, want the login command", err)
	}
}

func TestS3Index(t *testing.T) {
	root := fakeS3(t)
	writeFile(t, filepath.Join(root, "bucket", "den", "latest.json"), []byte(`{"version":"v1.7.2","notes":"n"}`))
	src := Source{S3Bucket: "bucket", S3Prefix: "den/"}
	ctx := context.Background()

	// A mirror that has not copied releases.json: the check reads latest.json,
	// and the picker says what to copy.
	if _, err := src.Index(ctx, "linux", "amd64"); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("err = %v, want ErrNoIndex", err)
	}
	res, err := src.Check(ctx, "linux", "amd64", "1.7.0", Policy{})
	if err != nil || res.Offer.Version != "v1.7.2" || res.Index != nil {
		t.Fatalf("check = %+v, %v", res, err)
	}
	if _, err := src.Releases(ctx, "linux", "amd64"); !errors.Is(err, ErrNoIndex) || !strings.Contains(err.Error(), "copy releases.json") {
		t.Fatalf("err = %v, want what to copy into the mirror", err)
	}

	writeFile(t, filepath.Join(root, "bucket", "den", IndexFile), []byte(indexJSON))
	rels, err := src.Releases(ctx, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if v := versions(rels); v != "v1.8.0-rc.1 v1.7.2 v1.7.0" {
		t.Fatalf("versions = %s", v)
	}
	if rels[1].URL != "s3://bucket/den/v1.7.2/den_1.7.2_linux_amd64.tar.gz" || !src.CanInstall(rels[1]) {
		t.Errorf("v1.7.2 = %+v", rels[1])
	}
	if res, err := src.Check(ctx, "linux", "amd64", "1.7.0", Policy{Channel: ChannelPrerelease}); err != nil || res.Offer.Version != "v1.8.0-rc.1" {
		t.Fatalf("check = %s, %v", res.Offer.Version, err)
	}
}

func TestS3IndexKeepsOtherFailures(t *testing.T) {
	fakeS3(t)
	t.Setenv("DEN_FAKE_S3_EXPIRED", "1")
	if _, err := (Source{S3Bucket: "bucket"}).Index(context.Background(), "linux", "amd64"); err == nil || errors.Is(err, ErrNoIndex) || !strings.Contains(err.Error(), "aws sso login") {
		t.Fatalf("err = %v, want the login named, not a missing index", err)
	}
}
