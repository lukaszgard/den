package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/runbook"
	vpnsvc "github.com/lukaszgard/den/internal/service/vpn"
)

// pathEntry is one file or folder den reads or writes, for `den paths`.
type pathEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Kind   string `json:"kind"` // "file" or "dir"
	Exists bool   `json:"exists"`
	Note   string `json:"note,omitempty"`
}

// pathsReport is `den paths --json`.
type pathsReport struct {
	// ConfigSource says where the den.yaml in use was found: the -c flag, the
	// current directory, the config directory, or the pre-XDG location.
	ConfigSource string      `json:"config_source,omitempty"`
	Paths        []pathEntry `json:"paths"`
}

func pathsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "paths",
		Short: "List the files and folders den reads and writes on this machine",
		Long: "Print every file and folder den uses here, and whether each exists: the den.yaml\n" +
			"in use and why that one, the config directory your own files belong in, the\n" +
			"runbook folders, the VPN settings files, and where den keeps its state and\n" +
			"downloaded data.\n\n" +
			"Like `go env`, it only reports; it never creates or changes anything. The\n" +
			"folders follow XDG_CONFIG_HOME, XDG_STATE_HOME and XDG_DATA_HOME, and\n" +
			"APPDATA and LOCALAPPDATA on Windows.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := buildPathsReport(cfgFile)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), report)
			}
			return writePaths(cmd.OutOrStdout(), report)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the paths as JSON")
	return cmd
}

func buildPathsReport(explicit string) pathsReport {
	var r pathsReport
	add := func(name, path, kind, note string) {
		if path == "" {
			return
		}
		_, err := os.Stat(path)
		e := pathEntry{Name: name, Path: path, Kind: kind, Exists: err == nil, Note: note}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			// Unreadable is not the same as absent: a root-owned folder says so.
			e.Note = joinNote(note, "cannot be read by you")
		}
		r.Paths = append(r.Paths, e)
	}

	cfgPath, source, err := config.DiscoverSource(explicit)
	var cfg *config.Config
	switch {
	case err != nil && explicit != "":
		add("den.yaml", explicit, "file", "the -c file")
	case err != nil:
		// Where a new one belongs: the config directory.
		add("den.yaml", filepath.Join(platform.ConfigDir(), "den.yaml"), "file",
			"none found; `den init -o` here, or copy den.yaml from the release archive")
	default:
		r.ConfigSource = source
		add("den.yaml", cfgPath, "file", "from "+source)
		if loaded, lerr := config.Load(cfgPath); lerr != nil {
			r.Paths[len(r.Paths)-1].Note = joinNote(r.Paths[len(r.Paths)-1].Note, "does not load: run `den config --check`")
		} else {
			cfg = loaded
		}
	}

	configDir := platform.ConfigDir()
	add("config directory", configDir, "dir", "your own files belong here")
	if configDir != "" {
		add("runbooks", filepath.Join(configDir, "runbooks"), "dir", "the usual place for runbooks you write")
		add("RDS CA bundle", filepath.Join(configDir, "global-bundle.pem"), "file", "downloaded for DocumentDB when needed")
	}
	add("state directory", platform.StateDir(), "dir", "run history, runbook trust")
	add("runbook drafts", runbook.UserDrafts().Dir(), "dir", "requests typed for an agent; may hold secrets")
	add("data directory", platform.DataDir(), "dir", "runbook source checkouts, runbook state")

	if cfg != nil {
		base := filepath.Dir(cfgPath)
		if abs, aerr := filepath.Abs(cfgPath); aerr == nil {
			base = filepath.Dir(abs)
		}
		if cfg.Runbooks != nil {
			for _, d := range cfg.Runbooks.Dirs {
				p := platform.ExpandHome(d)
				if !filepath.IsAbs(p) {
					p = filepath.Join(base, p)
				}
				if !hasPath(r.Paths, p) {
					add("runbooks (runbooks.dirs)", p, "dir", "")
				}
			}
		}
		for _, v := range cfg.VPNs {
			if p := v.ConfigFilePath(); p != "" {
				add("vpn "+v.Name, p, "file", "config_file")
			}
			if runtime.GOOS != "windows" {
				add("vpn "+v.Name+" helper copy", vpnsvc.HelperConfPath(vpnsvc.Slug(v.Name)), "file",
					"root-owned, made by `den vpn install-helper`")
			}
		}
	}
	return r
}

func hasPath(entries []pathEntry, p string) bool {
	for _, e := range entries {
		if e.Path == p {
			return true
		}
	}
	return false
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func writePaths(w io.Writer, r pathsReport) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, e := range r.Paths {
		state := "missing"
		if e.Exists {
			state = "exists"
		}
		note := e.Note
		if note != "" {
			note = "(" + note + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, state, platform.TildePath(e.Path), note)
	}
	return tw.Flush()
}
