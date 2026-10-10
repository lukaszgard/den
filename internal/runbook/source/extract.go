package source

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extract unpacks the tar git archive writes into dir, which exists and is
// empty. Nothing may land outside dir: an entry naming a parent or an
// absolute path, a link leading out of the tree, or a file written through a
// link fails the whole checkout. The repository is someone else's.
func extract(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	links := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the archive: %w", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue // git's commit id, not a file
		}
		name := filepath.FromSlash(strings.TrimSuffix(h.Name, "/"))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("%s: outside the repository", h.Name)
		}
		for p := filepath.Dir(name); p != "."; p = filepath.Dir(p) {
			if links[p] {
				return fmt.Errorf("%s: under the link %s", h.Name, filepath.ToSlash(p))
			}
		}
		target := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, h, tr); err != nil {
				return err
			}
		case tar.TypeSymlink:
			to := filepath.FromSlash(h.Linkname)
			if filepath.IsAbs(to) || !filepath.IsLocal(filepath.Join(filepath.Dir(name), to)) {
				return fmt.Errorf("%s: links outside the repository (%s)", h.Name, h.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
			links[name] = true
		default:
			return fmt.Errorf("%s: not a file, folder or link", h.Name)
		}
	}
}

// writeFile writes one regular file, executable when the repository says so.
func writeFile(target string, h *tar.Header, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	mode := os.FileMode(0o640)
	if h.Mode&0o111 != 0 {
		mode = 0o750
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, werr := io.Copy(f, r)
	return errors.Join(werr, f.Close())
}
