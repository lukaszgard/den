package runbook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook/source"
)

// ManifestName is the file that makes a folder a runbook. It holds the same
// keys as an entry of runbooks.items in den.yaml.
const ManifestName = source.Manifest

// loadManifest reads the runbook.yaml of dir. The name defaults to the folder's,
// and the runbook runs in the folder unless the manifest names a cwd, which is
// read against the folder.
//
// Unknown keys are an error, not ignored as den.yaml's are: a misspelt
// confirm: true would otherwise drop the safety gate without a word.
func loadManifest(dir string) (config.RunbookSpec, error) {
	spec, _, err := loadManifestNamed(dir)
	return spec, err
}

// loadManifestNamed is loadManifest, also saying whether the manifest named
// the runbook itself.
func loadManifestNamed(dir string) (config.RunbookSpec, bool, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return config.RunbookSpec{}, false, err
	}
	var spec config.RunbookSpec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil && !errors.Is(err, io.EOF) {
		return config.RunbookSpec{}, false, fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "yaml: "))
	}
	named := spec.Name != ""
	if !named {
		spec.Name = filepath.Base(dir)
	}
	spec.Source = path
	spec.Dir = dir
	spec.Cwd = resolvePath(spec.Cwd, dir)
	if spec.Cwd == "" {
		spec.Cwd = dir
	}
	return spec, named, nil
}
