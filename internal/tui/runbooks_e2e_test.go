//go:build !windows

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/termsession"
)

// runbookE2EEnv names the runbook folder TestHelperDenRunbooks shows.
const runbookE2EEnv = "DEN_RUNBOOK_E2E_DIR"

// TestHelperDenRunbooks is not a test: it is den with one runbook folder,
// started by TestRunbookViewEndToEnd. Its terminals are real shells.
func TestHelperDenRunbooks(t *testing.T) {
	root := os.Getenv(runbookE2EEnv)
	if root == "" {
		return
	}
	awsCfg := filepath.Join(t.TempDir(), "aws-config")
	if err := os.WriteFile(awsCfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AWS:          &config.AWSSpec{ConfigPath: awsCfg, CredentialsPath: awsCfg},
		Environments: map[string]config.EnvSpec{"dev": {AWSProfile: "dev-profile"}},
		Runbooks:     &config.RunbooksConfig{Dirs: []string{root}},
	}
	m := New(managerWith(), Options{
		ConfigPath:     filepath.Join(filepath.Dir(root), "den.yaml"),
		Config:         cfg,
		Version:        "test",
		RunbookHistory: runbook.NewHistory(filepath.Join(filepath.Dir(root), "state")),
	})
	if _, err := tea.NewProgram(e2eApp{m}).Run(); err != nil {
		t.Fatal(err)
	}
}

// The runbook view on a real terminal: a run's output in Logs, its folder and
// a file of it in Files, an editor whose tab closes with it, and a real shell
// opened in the runbook's folder with what den sets for it, which den stops
// when it quits.
func TestRunbookViewEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs den on a pty")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this machine")
	}
	base := t.TempDir()
	folder := filepath.Join(base, "runbooks", "greet")
	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		runbook.ManifestName: "name: Greet\ncommand: ./run.sh\nenv: dev\n",
		"run.sh":             "#!/bin/sh\necho \"hello from $(basename \"$PWD\")\"\n",
	} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// An editor that saves a line and quits, as :wq would.
	editor := filepath.Join(base, "edit")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\necho 'echo saved in the editor' >> \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := termsession.Start(termsession.Command{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestHelperDenRunbooks$"},
		// A home of its own, and a plain shell with a prompt to wait for.
		Env: []string{
			runbookE2EEnv + "=" + filepath.Dir(folder), "HOME=" + t.TempDir(),
			"SHELL=/bin/sh", "PS1=rb> ", "ENV=", "HISTFILE=", "VISUAL=" + editor,
		},
	}, 120, 36)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	den := screen{t: t, s: s}

	// Runbooks is the third entry of the default menu.
	den.waitText("Developer Environment Navigator")
	for range 2 {
		den.s.SendKey(vt.KeyPressEvent{Code: vt.KeyDown})
	}
	den.waitText("Run your scripts")
	den.typeText("l")
	den.waitText("Greet")
	den.enter()
	den.waitText("NEVER RUN")
	den.assertFrame(36)

	// r runs it in its folder; Logs shows the output.
	den.typeText("r")
	den.waitText("hello from greet")
	den.waitText("EXIT 0")
	den.assertFrame(36)

	// Files lists the folder as tree does, the run den kept folded into logs/.
	for range 3 {
		den.s.SendKey(vt.KeyPressEvent{Code: vt.KeyRight})
	}
	den.waitText("logs/  1 run · History")
	den.waitText("└── runbook.yaml")
	den.assertFrame(36)
	// enter shows the file picked in the tab; esc goes back to the tree.
	den.s.SendKey(vt.KeyPressEvent{Code: vt.KeyDown})
	den.enter()
	den.waitText("── run.sh")
	den.waitText("basename")
	den.assertFrame(36)
	// e opens it in the editor; when the editor quits, its tab goes with it,
	// back on the file shown, which shows what was saved.
	den.typeText("e")
	den.waitText("saved in the editor")
	den.waitGone("1 run.sh")
	den.waitText("esc: files")
	den.assertFrame(36)
	den.esc()
	// The tree is what shows that esc has been read: a frame without the file
	// can be a redraw in between, and a t typed right after an esc that has not
	// been read yet arrives with it as alt+t.
	den.waitText("└── runbook.yaml")
	den.waitGone("basename")

	// t opens a real shell in the runbook's folder, with its environment.
	den.typeText("t")
	den.waitText("rb>")
	den.waitText("TERMINAL")
	den.run(`echo "in=$(basename "$PWD") as=$AWS_PROFILE rb=$DEN_RUNBOOK pid=$$"`)
	text := den.waitText("in=greet as=dev-profile rb=Greet")
	den.assertFrame(36)
	pid := regexp.MustCompile(`pid=(\d+)`).FindStringSubmatch(text)

	// A shell that switches the arrows at its prompt, as zsh with oh-my-zsh
	// does, is still at its prompt: esc gives den the keys back, as at psql's.
	den.run(`printf '\033[?1h\033='`)
	den.waitText("esc/ctrl+t")
	den.esc()
	den.waitText("enter: type")
	den.waitGone("TERMINAL")
	// esc again goes back to the list, where the runbook shows its last run
	// and its running terminal.
	den.esc()
	text = den.waitText("⌨ 1")
	if !strings.Contains(text, "exit 0") {
		t.Fatalf("the list does not show the last run:\n%s", text)
	}

	// Quitting stops the shell with den. q quits from the list itself: an esc
	// right before it would arrive as alt+q.
	den.typeText("q")
	select {
	case <-s.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("den did not quit; it shows:\n%s", den.text())
	}
	if pid == nil {
		t.Fatal("the shell did not print its pid")
	}
	deadline := time.Now().Add(10 * time.Second)
	for exec.Command("kill", "-0", pid[1]).Run() == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the runbook's shell %s outlived den", pid[1])
		}
		time.Sleep(50 * time.Millisecond)
	}
}
