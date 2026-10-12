package den

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The settings both sizes share; the rest of the look is doubled in the HD file.
var tapeSettingsKept = map[string]bool{"WindowBar": true, "MarginFill": true, "FontFamily": true, "TypingSpeed": true}

// TestTapeLayout holds every recording under docs/ to the layout in AGENTS.md
// (Feature documentation): <name>-steps.tape holds the scenes, <name>.tape renders
// the README's GIF from them at the base size and <name>-hd.tape the site's MP4 at
// twice it. An MP4 rendered at the base size looks soft on the site, and a GIF tape
// with scenes of its own drifts from the video.
func TestTapeLayout(t *testing.T) {
	have := map[string]bool{}
	err := filepath.WalkDir("docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		p = filepath.ToSlash(p)
		// docs/tape-settings*.tape are the look, Sourced by the tapes below them.
		if !d.IsDir() && strings.HasSuffix(p, ".tape") && path.Dir(p) != "docs" {
			have[p] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(have) == 0 {
		t.Fatal("no tapes under docs/")
	}

	look := tapeSettings(t, "docs/tape-settings.tape")
	for p := range have {
		dir, name := path.Dir(p), strings.TrimSuffix(path.Base(p), ".tape")
		switch {
		case strings.HasSuffix(name, "-steps"):
			base := strings.TrimSuffix(name, "-steps")
			for _, twin := range []string{base + ".tape", base + "-hd.tape"} {
				if !have[dir+"/"+twin] {
					t.Errorf("%s: no %s/%s renders it (AGENTS.md, Feature documentation)", p, dir, twin)
				}
			}
			for _, cmd := range tapeCommands(t, p) {
				fields := strings.Fields(cmd)
				switch {
				case fields[0] == "Output" || fields[0] == "Source":
					t.Errorf("%s: %q belongs in %s.tape and %s-hd.tape, not in the steps", p, cmd, base, base)
				case fields[0] == "Set" && len(fields) > 1 && look[fields[1]] != "":
					t.Errorf("%s: %q is the look, set by docs/tape-settings*.tape; the GIF and the video would differ", p, cmd)
				}
			}
		case strings.HasSuffix(name, "-hd"):
			base := strings.TrimSuffix(name, "-hd")
			checkTapeWrapper(t, have, p, dir+"/"+base+".mp4", "docs/tape-settings-hd.tape", dir+"/"+base+"-steps.tape")
		default:
			checkTapeWrapper(t, have, p, dir+"/"+name+".gif", "docs/tape-settings.tape", dir+"/"+name+"-steps.tape")
		}
	}
}

// checkTapeWrapper fails unless the tape at p is only its Output, its settings
// and its steps, in that order.
func checkTapeWrapper(t *testing.T, have map[string]bool, p, output, settings, steps string) {
	t.Helper()
	want := []string{"Output " + output, "Source " + settings, "Source " + steps}
	if got := tapeCommands(t, p); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s: want only these commands (AGENTS.md, Feature documentation):\n%s\ngot:\n%s",
			p, strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
	if !have[steps] {
		t.Errorf("%s: %s is missing", p, steps)
	}
}

// TestTapeSettingsHDIsTwiceTheBase keeps the two looks in step: the HD file is the
// base one with the window, font, margin and radius doubled, so both render the
// same columns and rows and the TUI lays out alike in the GIF and the video.
func TestTapeSettingsHDIsTwiceTheBase(t *testing.T) {
	base := tapeSettings(t, "docs/tape-settings.tape")
	hd := tapeSettings(t, "docs/tape-settings-hd.tape")
	for key, b := range base {
		h, ok := hd[key]
		switch {
		case !ok:
			t.Errorf("docs/tape-settings-hd.tape: no Set %s (docs/tape-settings.tape has %s)", key, b)
		case tapeSettingsKept[key]:
			if h != b {
				t.Errorf("Set %s: %s in the HD settings, %s in the base ones; want the same", key, h, b)
			}
		default:
			bn, err1 := strconv.Atoi(b)
			hn, err2 := strconv.Atoi(h)
			if err1 != nil || err2 != nil || hn != 2*bn {
				t.Errorf("Set %s: %s in the HD settings, want twice the base %s", key, h, b)
			}
		}
	}
	for key := range hd {
		if _, ok := base[key]; !ok {
			t.Errorf("docs/tape-settings-hd.tape: Set %s is not in docs/tape-settings.tape", key)
		}
	}
}

// tapeSettings returns a settings tape's Set commands, key to value.
func tapeSettings(t *testing.T, p string) map[string]string {
	t.Helper()
	settings := map[string]string{}
	for _, cmd := range tapeCommands(t, p) {
		fields := strings.SplitN(cmd, " ", 3)
		if len(fields) != 3 || fields[0] != "Set" {
			t.Fatalf("%s: %q: a settings tape holds only Set commands", p, cmd)
		}
		settings[fields[1]] = fields[2]
	}
	return settings
}

// tapeCommands returns a tape's lines without blanks and comments.
func tapeCommands(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			cmds = append(cmds, line)
		}
	}
	return cmds
}
