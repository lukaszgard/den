package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// maxBinarySize bounds how much an archive entry may expand to. den is ~40 MB;
// anything near this is not a den binary, and the cap stops a corrupt or
// hostile archive from filling the disk. A variable only so tests can lower it.
var maxBinarySize int64 = 512 << 20

// Executable returns the path of the running den binary with symlinks resolved,
// which is the file an install replaces.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// TempFile creates an empty file named after pattern next to target, so the
// final rename stays on one filesystem, or in the system temp dir when that
// directory is not writable. The caller removes it.
func TempFile(target, pattern string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), pattern)
	if err != nil {
		if f, err = os.CreateTemp("", pattern); err != nil {
			return "", err
		}
	}
	path := f.Name()
	return path, f.Close()
}

// VerifyDownload checks the archive at path against the release's
// checksums.txt. A release without one — every release made before den
// verified downloads — passes with a warning the caller must show; a
// mismatch, or checksums that cannot be read, is an error.
func (s Source) VerifyDownload(ctx context.Context, rel Release, path string) (warning string, err error) {
	sums, err := s.Checksums(ctx, rel)
	switch {
	case errors.Is(err, ErrNoChecksums):
		return "This release publishes no " + ChecksumsFile + " — download not verified", nil
	case err != nil:
		return "", err
	}
	return "", Verify(path, rel.Archive, sums)
}

// Install puts the den binary of rel in place of exe: download, verify,
// extract, replace. onStep, when set, hears each step once it is done, a
// verification warning among them. exe is only touched by the last step, so a
// failure before it leaves the installed den as it was.
func (s Source) Install(ctx context.Context, rel Release, exe, goos string, onStep func(string)) error {
	step := func(text string) {
		if onStep != nil {
			onStep(text)
		}
	}
	if !s.CanInstall(rel) {
		return fmt.Errorf("release %s has no %s asset", rel.Version, rel.Archive)
	}
	archive, err := TempFile(exe, "den-download-*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	defer os.Remove(archive) //nolint:errcheck
	if err := s.Download(ctx, rel, archive, nil, nil); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	step("Downloaded " + rel.Archive)
	warning, err := s.VerifyDownload(ctx, rel, archive)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if warning != "" {
		step("⚠ " + warning)
	} else {
		step("Verified SHA-256 against " + ChecksumsFile)
	}
	bin, err := ExtractBinary(archive, goos, exe)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	step("Extracted the den binary")
	if err := Replace(bin, exe); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}
	step("Replaced " + exe)
	return nil
}

// ExtractBinary copies the den binary out of the archive at archivePath into a
// new executable temp file next to target and returns its path. Windows
// archives are zips holding den.exe; every other platform ships a tar.gz
// holding den.
func ExtractBinary(archivePath, goos, target string) (string, error) {
	if goos == "windows" {
		return extractZip(archivePath, "den.exe", target)
	}
	return extractTarGz(archivePath, "den", target)
}

func extractTarGz(archivePath, name, target string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close() //nolint:errcheck

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("binary %q not found in archive", name)
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == name {
			return writeExecutable(tr, target)
		}
	}
}

func extractZip(archivePath, name, target string) (string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer r.Close() //nolint:errcheck
	for _, f := range r.File {
		if filepath.Base(f.Name) != name || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		path, err := writeExecutable(rc, target)
		rc.Close() //nolint:errcheck
		return path, err
	}
	return "", fmt.Errorf("binary %q not found in archive", name)
}

// writeExecutable copies at most maxBinarySize bytes of src into a new
// executable temp file next to target.
func writeExecutable(src io.Reader, target string) (string, error) {
	path, err := TempFile(target, "den-new-*")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		os.Remove(path) //nolint:errcheck
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fail(err)
	}
	n, err := io.Copy(f, io.LimitReader(src, maxBinarySize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fail(err)
	}
	if n > maxBinarySize {
		return fail(fmt.Errorf("archive entry exceeds %d MB", maxBinarySize>>20))
	}
	if n == 0 {
		return fail(fmt.Errorf("archive entry is empty"))
	}
	if err := os.Chmod(path, 0o755); err != nil { //nolint:gosec // the new binary must be executable
		return fail(err)
	}
	return path, nil
}

// Replace moves the new binary at src over target. A plain rename is atomic
// but fails across filesystems; then the binary is copied next to target first
// and renamed from there. src is gone afterwards either way.
func Replace(src, target string) error {
	if err := os.Rename(src, target); err == nil {
		return nil
	}
	defer os.Remove(src) //nolint:errcheck
	tmp, err := TempFile(target, "den-replace-*")
	if err != nil {
		return err
	}
	if err := copyFile(src, tmp); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // the new binary must be executable
		os.Remove(tmp) //nolint:errcheck
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close() //nolint:errcheck
		return err
	}
	return out.Close()
}
