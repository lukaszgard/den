package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/prereq"
	"github.com/lukaszgard/den/internal/termsession"
)

// fakeBin puts executables called names on a PATH of their own.
func fakeBin(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestFind(t *testing.T) {
	dir := fakeBin(t, "codex")
	if kind, path, err := Find(Auto); err != nil || kind != Codex || path != filepath.Join(dir, "codex") {
		t.Fatalf("auto with only codex = %s %s %v", kind, path, err)
	}
	if _, _, err := Find(Claude); err == nil || !strings.Contains(err.Error(), "claude not found") {
		t.Fatalf("claude missing = %v, want an error with its install hint", err)
	}
	dir = fakeBin(t, "claude", "codex")
	if kind, _, err := Find(""); err != nil || kind != Claude {
		t.Fatalf("auto with both = %s %v, want claude first", kind, err)
	}
	if kind, path, err := Find(Codex); err != nil || kind != Codex || path != filepath.Join(dir, "codex") {
		t.Fatalf("codex = %s %s %v", kind, path, err)
	}
	fakeBin(t)
	if _, _, err := Find(Auto); err == nil || !strings.Contains(err.Error(), "no agent found: install one of Claude Code, Codex") {
		t.Fatalf("no agent = %v, want every agent named", err)
	}
	if _, _, err := Find("gemini"); err == nil {
		t.Fatal("an unknown agent was accepted")
	}
}

var opts = Options{
	Den:     "/usr/local/bin/den",
	Config:  "/home/me/.config/den/den.yaml",
	Dir:     "/home/me/.config/den/runbooks/rds-status",
	Guide:   "# Writing a den runbook\n\nUse \"quotes\", <tags> & tabs\there.",
	Request: "check the status of my RDS instances, env eu-staging, in Rust",
}

func mustCommand(t *testing.T, a Agent, o Options) termsession.Command {
	t.Helper()
	cmd, err := Command(a, o)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestClaudeCommand(t *testing.T) {
	cmd := mustCommand(t, Agent{Kind: Claude, Path: "/bin/claude"}, opts)
	if cmd.Path != "/bin/claude" || cmd.Dir != opts.Dir {
		t.Fatalf("command = %s in %s", cmd.Path, cmd.Dir)
	}
	args := cmd.Args
	if args[0] != opts.Request {
		t.Fatalf("the request must come first, before the options that take many values: %q", args)
	}
	value := func(flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		t.Fatalf("no %s in %q", flag, args)
		return ""
	}
	var mcp struct {
		McpServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(value("--mcp-config")), &mcp); err != nil {
		t.Fatal(err)
	}
	den := mcp.McpServers["den"]
	if den.Command != opts.Den || strings.Join(den.Args, " ") != "mcp -c "+opts.Config {
		t.Fatalf("den's MCP server = %+v", den)
	}
	if sp := value("--append-system-prompt"); !strings.HasPrefix(sp, opts.Guide) || !strings.Contains(sp, opts.Dir) {
		t.Fatalf("system prompt = %q", sp)
	}
	if got := value("--allowedTools"); got != "mcp__den__runbook_guide,mcp__den__validate_runbook" {
		t.Fatalf("pre-allowed tools = %q, want only den's read-only ones", got)
	}
}

func TestCodexCommand(t *testing.T) {
	cmd := mustCommand(t, Agent{Kind: Codex, Path: "/bin/codex"}, opts)
	args := cmd.Args
	if args[len(args)-1] != opts.Request || cmd.Dir != opts.Dir {
		t.Fatalf("codex args = %q in %s", args, cmd.Dir)
	}
	settings := map[string]string{}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" {
			key, val, _ := strings.Cut(args[i+1], "=")
			settings[key] = val
		}
	}
	// A TOML basic string is a JSON string with no raw control characters:
	// each value must read back as JSON and hold none.
	for key, val := range settings {
		for _, r := range val {
			if unicode.IsControl(r) {
				t.Errorf("%s holds a raw control character: not a valid TOML string", key)
			}
		}
	}
	var command, instructions string
	var server []string
	for key, into := range map[string]any{"mcp_servers.den.command": &command, "mcp_servers.den.args": &server, "developer_instructions": &instructions} {
		if err := json.Unmarshal([]byte(settings[key]), into); err != nil {
			t.Fatalf("%s = %q: %v", key, settings[key], err)
		}
	}
	if command != opts.Den || strings.Join(server, " ") != "mcp -c "+opts.Config || !strings.Contains(instructions, `Use "quotes", <tags> & tabs`) {
		t.Fatalf("codex got command %q, args %q, instructions %.80q", command, server, instructions)
	}
	if strings.Contains(settings["developer_instructions"], "\\u003c") {
		t.Error("< was escaped: the agent would read \\u003c")
	}

	if noPrompt := mustCommand(t, Agent{Kind: Codex, Path: "/bin/codex"}, Options{Den: "den", Dir: "/x"}); len(noPrompt.Args) != 6 {
		t.Fatalf("without a request codex gets only its settings: %q", noPrompt.Args)
	}
}

func TestResolve(t *testing.T) {
	dir := fakeBin(t, "claude", "codex", "ollama")
	cases := []struct {
		spec config.AgentSpec
		want Agent
	}{
		{config.AgentSpec{Name: "Opus", Command: "claude --model opus"},
			Agent{Name: "Opus", Kind: Claude, Path: filepath.Join(dir, "claude"), Args: []string{"--model", "opus"}}},
		{config.AgentSpec{Name: "Codex", Command: "codex"},
			Agent{Name: "Codex", Kind: Codex, Path: filepath.Join(dir, "codex"), Args: []string{}}},
		{config.AgentSpec{Name: "GLM", Command: "ollama launch claude --model glm-5:cloud"},
			Agent{Name: "GLM", Kind: Claude, Path: filepath.Join(dir, "ollama"), Args: []string{"launch", "claude", "--model", "glm-5:cloud"}, Separator: true}},
		{config.AgentSpec{Name: "Gemma", Command: "ollama launch codex"},
			Agent{Name: "Gemma", Kind: Codex, Path: filepath.Join(dir, "ollama"), Args: []string{"launch", "codex"}, Separator: true}},
		// An explicit kind wins, and the first word is still the program.
		// Only `ollama launch` takes the "--" before den's arguments.
		{config.AgentSpec{Name: "Wrapped", Command: "ollama run x", Kind: Codex},
			Agent{Name: "Wrapped", Kind: Codex, Path: filepath.Join(dir, "ollama"), Args: []string{"run", "x"}}},
		// An absolute path is taken as it is.
		{config.AgentSpec{Name: "Abs", Command: filepath.Join(dir, "claude") + " -p"},
			Agent{Name: "Abs", Kind: Claude, Path: filepath.Join(dir, "claude"), Args: []string{"-p"}}},
	}
	for _, c := range cases {
		got, err := Resolve(c.spec)
		if err != nil {
			t.Errorf("%s: %v", c.spec.Command, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Resolve(%q) = %+v, want %+v", c.spec.Command, got, c.want)
		}
	}

	fakeBin(t, "claude")
	if _, err := Resolve(config.AgentSpec{Name: "GLM", Command: "ollama launch claude"}); err == nil || !strings.Contains(err.Error(), "ollama not found") {
		t.Errorf("a missing wrapper = %v, want it named", err)
	}
	if _, err := Resolve(config.AgentSpec{Name: "X", Command: "codex"}); err == nil || !strings.Contains(err.Error(), "codex not found") {
		t.Errorf("a missing codex = %v, want its install hint", err)
	}
	if _, err := Resolve(config.AgentSpec{Name: "X", Command: "mystery --flag"}); err == nil || !strings.Contains(err.Error(), "set kind") {
		t.Errorf("an unknown kind = %v", err)
	}
}

func TestChoices(t *testing.T) {
	dir := fakeBin(t, "codex")
	agents, def, err := Choices(nil, "")
	if err != nil || def != 0 || len(agents) != 1 || agents[0].Name != Codex || agents[0].Path != filepath.Join(dir, "codex") {
		t.Fatalf("without entries = %+v %d %v, want today's Find as one choice", agents, def, err)
	}
	if _, _, err := Choices(nil, Claude); err == nil || !strings.Contains(err.Error(), "claude not found") {
		t.Fatalf("without entries, a missing claude = %v", err)
	}

	specs := []config.AgentSpec{
		{Name: "Sonnet", Command: "claude --model sonnet"},
		{Name: "Gone", Command: "ollama launch claude"},
		{Name: "Opus", Command: "claude --model opus"},
	}
	fakeBin(t, "claude")
	agents, def, err = Choices(specs, "Opus")
	if err != nil || len(agents) != 2 || agents[0].Name != "Sonnet" || agents[1].Name != "Opus" || def != 1 {
		t.Fatalf("Choices = %+v default %d %v, want the two that resolve, in order, Opus the default", agents, def, err)
	}
	if _, def, _ = Choices(specs, "nope"); def != 0 {
		t.Errorf("a preferred name that is no entry gave default %d, want the first", def)
	}
	// claude is no entry: the built-in is one more choice and the default, as
	// Select, and so the command line, would start it.
	agents, def, err = Choices(specs, "claude")
	if err != nil || len(agents) != 3 || def != 2 || agents[2].Name != Claude || len(agents[2].Args) != 0 {
		t.Errorf("preferred claude = %+v default %d %v, want the built-in appended as the default", agents, def, err)
	}
	// An entry of that name is the one preferred, nothing is appended.
	agents, def, _ = Choices([]config.AgentSpec{{Name: "Sonnet", Command: "claude"}, {Name: "claude", Command: "claude -p"}}, "claude")
	if len(agents) != 2 || def != 1 {
		t.Errorf("preferred claude entry = %+v default %d", agents, def)
	}
	// Not installed: the entries stay as they were.
	if agents, def, _ = Choices(specs[:1], "codex"); len(agents) != 1 || def != 0 {
		t.Errorf("preferred codex, not installed = %+v default %d", agents, def)
	}

	fakeBin(t)
	if _, _, err := Choices(specs, ""); err == nil || !strings.Contains(err.Error(), "claude not found") || !strings.Contains(err.Error(), "ollama not found") {
		t.Fatalf("none resolving = %v, want what is missing named", err)
	}
}

func TestSelect(t *testing.T) {
	dir := fakeBin(t, "claude", "codex")
	specs := []config.AgentSpec{
		{Name: "Opus", Command: "claude --model opus"},
		{Name: "codex", Command: "codex --model x"},
	}
	for name, want := range map[string]string{"": "Opus", "auto": "Opus", "Opus": "Opus", "codex": "codex"} {
		a, err := Select(specs, name)
		if err != nil || a.Name != want {
			t.Errorf("Select(%q) = %+v %v, want %s", name, a, err, want)
		}
	}
	// An entry named like a built-in shadows it.
	if a, _ := Select(specs, "codex"); len(a.Args) != 2 {
		t.Errorf("the codex entry did not win: %+v", a)
	}
	// Without such an entry the built-in name is the built-in.
	a, err := Select(specs[:1], "codex")
	if err != nil || a.Name != Codex || a.Path != filepath.Join(dir, "codex") || len(a.Args) != 0 {
		t.Errorf("built-in codex beside other entries = %+v %v", a, err)
	}
	if _, err := Select(specs, "nope"); err == nil || !strings.Contains(err.Error(), "Opus, codex") {
		t.Errorf("an unknown name = %v, want the entries named", err)
	}
	if _, err := Select(nil, "nope"); err == nil || strings.Contains(err.Error(), "runbooks.agents") {
		t.Errorf("an unknown name without entries = %v", err)
	}
	fakeBin(t)
	if _, err := Select(specs, "Opus"); err == nil {
		t.Error("an entry whose program is missing was started")
	}
}

func TestCommandPrefixAndSeparator(t *testing.T) {
	req := Options{Den: "den", Dir: "/x", Request: "do it"}
	for _, c := range []struct {
		a    Agent
		want []string // the start of the arguments
	}{
		{Agent{Kind: Claude, Path: "claude", Args: []string{"--model", "opus"}}, []string{"--model", "opus", "do it", "--mcp-config"}},
		{Agent{Kind: Claude, Path: "ollama", Args: []string{"launch", "claude", "--model", "m"}, Separator: true}, []string{"launch", "claude", "--model", "m", "--", "do it", "--mcp-config"}},
		{Agent{Kind: Codex, Path: "codex", Args: []string{"--model", "x"}}, []string{"--model", "x", "-c"}},
		{Agent{Kind: Codex, Path: "ollama", Args: []string{"launch", "codex"}, Separator: true}, []string{"launch", "codex", "--", "-c"}},
	} {
		cmd := mustCommand(t, c.a, req)
		if cmd.Path != c.a.Path || !reflect.DeepEqual(cmd.Args[:len(c.want)], c.want) {
			t.Errorf("%s args start %q, want %q", c.a.Path, cmd.Args[:len(c.want)], c.want)
		}
	}
	// Command must not write into the agent's own arguments.
	a := Agent{Kind: Claude, Path: "claude", Args: make([]string, 1, 10)}
	mustCommand(t, a, req)
	mustCommand(t, a, req)
	if len(a.Args) != 1 {
		t.Fatal("Command changed the agent's arguments")
	}
}

// A wrapper given by a relative path is read against the directory den runs in
// and handed over absolute: the agent starts in the runbook's folder, where
// ./ai-claude would not be found.
func TestResolveMakesARelativeCommandAbsolute(t *testing.T) {
	fakeBin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ai-claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, err := Resolve(config.AgentSpec{Name: "Team", Command: "./ai-claude --fast", Kind: Claude})
	// The temporary directory may sit behind a symlink; Abs does not resolve it.
	want, _ := filepath.Abs("ai-claude")
	if err != nil || got.Path != want || !filepath.IsAbs(got.Path) || !reflect.DeepEqual(got.Args, []string{"--fast"}) {
		t.Fatalf("Resolve = %+v %v, want %s with --fast", got, err, want)
	}
}

// ~ opens a command's path, and a missing program is named as it was typed.
func TestResolveExpandsHomeAndNamesWhatWasTyped(t *testing.T) {
	fakeBin(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bin", "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(config.AgentSpec{Name: "Mine", Command: "~/bin/claude -p"})
	if err != nil || got.Path != filepath.Join(home, "bin", "claude") || got.Kind != Claude {
		t.Fatalf("Resolve = %+v %v, want the program under ~", got, err)
	}
	_, err = Resolve(config.AgentSpec{Name: "Gone", Command: "~/nowhere/claude", Kind: Claude})
	if err == nil || !strings.Contains(err.Error(), "~/nowhere/claude not found") {
		t.Errorf("a missing program under ~ = %v, want it named as typed", err)
	}
	_, err = Resolve(config.AgentSpec{Name: "Gone", Command: "./nowhere/claude", Kind: Claude})
	if err == nil || !strings.Contains(err.Error(), "./nowhere/claude not found on PATH") {
		t.Errorf("a missing relative program = %v, want it named as typed", err)
	}
}

// A runbook's agent setting is a name den knows or a command line: either
// starts, and a command starts again from its own name.
func TestPick(t *testing.T) {
	dir := fakeBin(t, "claude", "codex", "ollama")
	specs := []config.AgentSpec{{Name: "Claude Opus", Command: "claude --model opus"}}

	a, err := Pick(specs, "Claude Opus")
	if err != nil || a.Typed || a.Name != "Claude Opus" || a.Label() != "Claude Opus" || !reflect.DeepEqual(a.Args, []string{"--model", "opus"}) {
		t.Fatalf("a runbooks.agents name = %+v, %v", a, err)
	}
	if a, err = Pick(specs, "codex"); err != nil || a.Kind != Codex || a.Path != filepath.Join(dir, "codex") {
		t.Fatalf("codex = %+v, %v", a, err)
	}
	if a, err = Pick(nil, ""); err != nil || a.Kind != Claude {
		t.Fatalf("empty = %+v, %v, want the default, claude first", a, err)
	}

	a, err = Pick(specs, " claude --model sonnet ")
	if err != nil || !a.Typed || a.Kind != Claude || a.Name != "claude --model sonnet" ||
		!reflect.DeepEqual(a.Args, []string{"--model", "sonnet"}) || a.Label() != "claude sonnet" {
		t.Fatalf("a command line = %+v, %v", a, err)
	}
	again, err := Pick(specs, a.Name)
	if err != nil || !reflect.DeepEqual(again, a) {
		t.Fatalf("Pick(a.Name) = %+v, %v, want the same agent: a tab restarts from its name", again, err)
	}
	a, err = Pick(nil, "ollama launch claude --model glm-5:cloud")
	if err != nil || !a.Separator || a.Label() != "claude glm-5:cloud" {
		t.Fatalf("a wrapper = %+v, %v", a, err)
	}
	if a, _ = Pick(nil, "claude --model=opus"); a.Label() != "claude opus" {
		t.Errorf("--model=opus labelled %q", a.Label())
	}
	if a, _ = Pick(nil, "claude -p"); a.Label() != "claude" {
		t.Errorf("a command without a model labelled %q", a.Label())
	}

	if _, err = Pick(nil, "./my-agent.sh"); err == nil || !strings.Contains(err.Error(), "set kind") {
		t.Errorf("a command of no known kind = %v, want it to say so", err)
	}
	if _, err = Pick(nil, "claude-nope --model x"); err == nil {
		t.Error("a program that is not there was accepted")
	}
}

// Changing a runbook tells the agent it exists; writing one tells it den made
// a starting point.
func TestEditInstructions(t *testing.T) {
	write, edit := opts.Instructions(), Options{Dir: opts.Dir, Guide: opts.Guide, Edit: true}.Instructions()
	if !strings.Contains(write, "den created it") || strings.Contains(edit, "den created it") {
		t.Fatalf("starting point text: write %v, edit %v", strings.Contains(write, "den created it"), strings.Contains(edit, "den created it"))
	}
	for _, want := range []string{"changing the den runbook in " + opts.Dir, "validate_runbook", "# Writing a den runbook"} {
		if !strings.Contains(edit, want) {
			t.Errorf("edit instructions lack %q:\n%s", want, edit)
		}
	}
	cmd := mustCommand(t, Agent{Kind: Claude, Path: "/bin/claude"}, Options{Dir: opts.Dir, Guide: opts.Guide, Edit: true, Request: "add a retry"})
	if !slices.ContainsFunc(cmd.Args, func(a string) bool { return strings.Contains(a, "changing the den runbook") }) {
		t.Errorf("the command does not carry the edit instructions: %q", cmd.Args)
	}
}

// Every name in config.AgentKinds needs a spec here, an install recipe and a
// `den doctor --for` feature, or a kind validates and then fails to start.
func TestEveryKindIsComplete(t *testing.T) {
	for _, k := range config.AgentKinds {
		t.Run(k, func(t *testing.T) {
			if spec, ok := kinds[k]; !ok || spec.title == "" || spec.args == nil {
				t.Errorf("kinds[%q] = %+v, want a title and args", k, spec)
			}
			if _, ok := prereq.Lookup(k); !ok {
				t.Errorf("the prereq catalog has no %q", k)
			}
			if _, ok := prereq.Features[k]; !ok {
				t.Errorf("prereq.Features has no %q", k)
			}
		})
	}
	if len(kinds) != len(config.AgentKinds) {
		t.Errorf("kinds has %d entries, config.AgentKinds %d", len(kinds), len(config.AgentKinds))
	}
}

func TestCommandUnknownKind(t *testing.T) {
	if _, err := Command(Agent{Name: "X", Kind: "gemini"}, opts); err == nil {
		t.Error("a kind den cannot start was accepted")
	}
}
