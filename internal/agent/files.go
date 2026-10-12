package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/lukaszgard/den/internal/platform"
)

// filesMaxAge is how long a start's setting files outlive their last use.
// An agent reads them while it runs, so they cannot go when den lets it go.
const filesMaxAge = 30 * 24 * time.Hour

// sessionFiles writes the setting files of one start (OpenCode's config,
// Copilot's AGENTS.md) into a folder of its own under root. The folder is
// named after the kind and everything the files are made from: starting the
// same agent again with the same request reuses it, and two runbooks, whose
// instructions name their folders, never share one. A file's path goes into
// the next file, so the name cannot come from the files themselves.
type sessionFiles struct {
	root, dir string
	used      bool
}

func newSessionFiles(root, kind string, inputs ...string) *sessionFiles {
	if root == "" {
		root = filepath.Join(platform.StateDir(), "agents")
	}
	h := sha256.New()
	h.Write([]byte(kind))
	for _, in := range inputs {
		h.Write([]byte{0})
		h.Write([]byte(in))
	}
	return &sessionFiles{root: root, dir: filepath.Join(root, hex.EncodeToString(h.Sum(nil))[:16])}
}

// write puts content at name in the session's folder and returns its
// absolute path. They hold the guide and paths, no credential, but are the
// user's alone all the same.
func (f *sessionFiles) write(name, content string) (string, error) {
	path, err := filepath.Abs(filepath.Join(f.dir, name))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	f.used = true
	return path, nil
}

// done marks the session's folder as just used and removes the folders of
// starts older than filesMaxAge. Rewriting a file leaves its folder's time
// as it was, hence the Chtimes. It is best effort: the agent has its files
// whatever happens here.
func (f *sessionFiles) done(now time.Time) {
	if !f.used {
		return
	}
	_ = os.Chtimes(f.dir, now, now)
	entries, err := os.ReadDir(f.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(f.root, e.Name())
		// Only folders named as den names them: a Files pointed at the wrong
		// place must not empty it.
		if !e.IsDir() || path == f.dir || !isSessionName(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > filesMaxAge {
			_ = os.RemoveAll(path)
		}
	}
}

func isSessionName(name string) bool {
	if len(name) != 16 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}
