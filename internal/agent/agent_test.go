package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
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

// sessionDir is the one folder a start wrote its setting files in.
func sessionDir(t *testing.T, files string) string {
	t.Helper()
	entries, err := os.ReadDir(files)
	if err != nil || len(entries) != 1 {
		t.Fatalf("files holds %v (%v), want one session folder", entries, err)
	}
	return filepath.Join(files, entries[0].Name())
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// OpenCode and Copilot CLI read den's instructions from files den writes for
// the start, take its MCP server for that session only and the request as
// their first prompt.
func TestFileAgentCommands(t *testing.T) {
	t.Setenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS", "")
	server := []string{opts.Den, "mcp", "-c", opts.Config}
	copilotMCP := `{"mcpServers":{"den":{"args":["mcp","-c","` + opts.Config + `"],"command":"` + opts.Den + `","tools":["*"],"type":"local"}}}`
	for _, c := range []struct {
		kind string
		// want are den's arguments without and then with the request.
		want, request []string
		env           func(dir string) string
		check         func(t *testing.T, dir string)
	}{
		{
			kind: OpenCode, request: []string{"--prompt", opts.Request},
			env: func(dir string) string { return "OPENCODE_CONFIG=" + filepath.Join(dir, "opencode", "opencode.json") },
			check: func(t *testing.T, dir string) {
				var cfg struct {
					Schema string `json:"$schema"`
					MCP    map[string]struct {
						Type    string
						Command []string
						Enabled bool
					}
					Instructions []string
				}
				if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "opencode", "opencode.json"))), &cfg); err != nil {
					t.Fatal(err)
				}
				den := cfg.MCP["den"]
				if cfg.Schema != "https://opencode.ai/config.json" || len(cfg.MCP) != 1 || den.Type != "local" || !den.Enabled || !reflect.DeepEqual(den.Command, server) {
					t.Errorf("opencode.json = %+v, want den's server %q alone", cfg, server)
				}
				instructions := filepath.Join(dir, "opencode", "instructions.md")
				if !reflect.DeepEqual(cfg.Instructions, []string{instructions}) {
					t.Errorf("instructions = %q, want %s", cfg.Instructions, instructions)
				}
				if got := readFile(t, instructions); got != opts.Instructions() {
					t.Errorf("instructions.md = %q", got)
				}
			},
		},
		{
			kind: Copilot,
			want: []string{"--additional-mcp-config", copilotMCP, "--allow-tool=den(runbook_guide)", "--allow-tool=den(validate_runbook)"},
			request: []string{"--additional-mcp-config", copilotMCP, "--allow-tool=den(runbook_guide)", "--allow-tool=den(validate_runbook)",
				"-i", opts.Request},
			env: func(dir string) string { return "COPILOT_CUSTOM_INSTRUCTIONS_DIRS=" + filepath.Join(dir, "copilot") },
			check: func(t *testing.T, dir string) {
				if got := readFile(t, filepath.Join(dir, "copilot", "AGENTS.md")); got != opts.Instructions() {
					t.Errorf("AGENTS.md = %q", got)
				}
			},
		},
	} {
		t.Run(c.kind, func(t *testing.T) {
			o := opts
			o.Files = t.TempDir()
			cmd := mustCommand(t, Agent{Kind: c.kind, Path: "/bin/" + c.kind}, o)
			dir := sessionDir(t, o.Files)
			if cmd.Path != "/bin/"+c.kind || cmd.Dir != opts.Dir || !reflect.DeepEqual(cmd.Args, c.request) {
				t.Errorf("command = %s %q in %s, want %q", cmd.Path, cmd.Args, cmd.Dir, c.request)
			}
			if want := []string{c.env(dir)}; !reflect.DeepEqual(cmd.Env, want) {
				t.Errorf("env = %q, want %q", cmd.Env, want)
			}
			c.check(t, dir)
			if runtime.GOOS != "windows" {
				err := filepath.Walk(o.Files, func(path string, info os.FileInfo, err error) error {
					if err == nil && path != o.Files && info.Mode().Perm()&0o077 != 0 {
						t.Errorf("%s is %v: other users can read it", path, info.Mode().Perm())
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}

			o.Request = ""
			if cmd := mustCommand(t, Agent{Kind: c.kind, Path: c.kind}, o); !reflect.DeepEqual(cmd.Args, c.want) {
				t.Errorf("without a request = %q, want %q", cmd.Args, c.want)
			}
			o.Request = opts.Request
			wrapped := mustCommand(t, Agent{Kind: c.kind, Path: "ollama", Args: []string{"launch", c.kind}, Separator: true}, o)
			if want := append([]string{"launch", c.kind, "--"}, c.request...); !reflect.DeepEqual(wrapped.Args, want) {
				t.Errorf("ollama launch = %q, want %q", wrapped.Args, want)
			}
		})
	}

	o := opts
	o.Files = t.TempDir()
	t.Setenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS", "/mine,/ours")
	cmd := mustCommand(t, Agent{Kind: Copilot, Path: "copilot"}, o)
	if want := "COPILOT_CUSTOM_INSTRUCTIONS_DIRS=" + filepath.Join(sessionDir(t, o.Files), "copilot") + ",/mine,/ours"; cmd.Env[0] != want {
		t.Errorf("env = %q, want the user's folders kept after den's: %q", cmd.Env, want)
	}
}

// One start's files go in a folder of its own, which the same start reuses;
// folders unused for 30 days go, and nothing den did not name is touched.
func TestSessionFolders(t *testing.T) {
	o := opts
	o.Files = t.TempDir()
	a := Agent{Kind: OpenCode, Path: "opencode"}
	first := mustCommand(t, a, o)
	dir := sessionDir(t, o.Files)
	if again := mustCommand(t, a, o); !reflect.DeepEqual(again.Env, first.Env) {
		t.Errorf("the same start wrote %q, then %q", first.Env, again.Env)
	}

	old := time.Now().Add(-31 * 24 * time.Hour)
	for _, name := range []string{"0123456789abcdef", "fedcba9876543210", "not-dens-folder"} {
		if err := os.Mkdir(filepath.Join(o.Files, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"0123456789abcdef", "not-dens-folder", filepath.Base(dir)} {
		if err := os.Chtimes(filepath.Join(o.Files, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	o.Dir = "/home/me/.config/den/runbooks/other"
	other := mustCommand(t, a, o)
	if reflect.DeepEqual(other.Env, first.Env) {
		t.Fatal("two runbooks share a folder")
	}
	entries, _ := os.ReadDir(o.Files)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if slices.Contains(names, "0123456789abcdef") || slices.Contains(names, filepath.Base(dir)) {
		t.Errorf("folders unused for 31 days are left: %q", names)
	}
	if !slices.Contains(names, "fedcba9876543210") || !slices.Contains(names, "not-dens-folder") || len(names) != 3 {
		t.Errorf("folders = %q, want the new one, the fresh one and the one den did not name", names)
	}

	// Rewriting its files leaves a folder's time as it was: a start marks
	// its folder used, so the next one's pruning keeps it.
	inUse := sessionFolderOf(t, other)
	if err := os.Chtimes(inUse, old, old); err != nil {
		t.Fatal(err)
	}
	mustCommand(t, a, o)
	if info, err := os.Stat(inUse); err != nil || time.Since(info.ModTime()) > time.Hour {
		t.Errorf("the reused folder was not marked used: %v %v", info, err)
	}
}

// sessionFolderOf is the session folder an OpenCode command's config is in.
func sessionFolderOf(t *testing.T, cmd termsession.Command) string {
	t.Helper()
	cfg, ok := strings.CutPrefix(cmd.Env[0], "OPENCODE_CONFIG=")
	if !ok {
		t.Fatalf("env = %q", cmd.Env)
	}
	return filepath.Dir(filepath.Dir(cfg))
}

// Qwen Code, Droid, Pi and Kimi Code take no MCP server for one session: their
// instructions send them to den runbook validate instead, quoted for a shell.
// Kimi Code takes no first prompt either, so the request is in its
// instructions and the user types go.
func TestCLIFallbackCommands(t *testing.T) {
	o := opts
	o.Dir = "/home/me/my runbooks/rds-status"
	validate := "`/usr/local/bin/den runbook validate -c /home/me/.config/den/den.yaml '/home/me/my runbooks/rds-status'`"
	for _, c := range []struct {
		kind string
		// want are den's arguments, DIR standing for the session's folder and
		// TEXT for the instructions.
		want  []string
		files []string // the files written, under DIR
	}{
		{Qwen, []string{"--append-system-prompt", "TEXT", "-i", opts.Request}, nil},
		{Droid, []string{"--append-system-prompt-file", "DIR/droid/instructions.md", opts.Request}, []string{"droid/instructions.md"}},
		{Pi, []string{"--append-system-prompt", "DIR/pi/instructions.md", opts.Request}, []string{"pi/instructions.md"}},
		{Kimi, []string{"--agent-file", "DIR/kimi/den-runbook.md"}, []string{"kimi/den-runbook.md", "kimi/instructions.md"}},
	} {
		t.Run(c.kind, func(t *testing.T) {
			o := o
			o.Files = t.TempDir()
			cmd := mustCommand(t, Agent{Kind: c.kind, Path: c.kind}, o)
			text := instructions(o, kinds[c.kind])
			dir := ""
			if c.files != nil {
				dir = sessionDir(t, o.Files)
			} else if entries, _ := os.ReadDir(o.Files); len(entries) != 0 {
				t.Errorf("%s wrote files it does not read: %v", c.kind, entries)
			}
			var want []string
			for _, w := range c.want {
				if rest, ok := strings.CutPrefix(w, "DIR/"); ok {
					w = filepath.Join(dir, filepath.FromSlash(rest))
				}
				if w == "TEXT" {
					w = text
				}
				want = append(want, w)
			}
			if !reflect.DeepEqual(cmd.Args, want) || cmd.Env != nil {
				t.Errorf("args = %q env %q, want %q", cmd.Args, cmd.Env, want)
			}
			for _, f := range c.files {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Error(err)
				}
			}

			for _, w := range []string{"Writing a den runbook", "run " + validate + " in your shell until it reports the runbook ready",
				"You have no den MCP tools in this session"} {
				if !strings.Contains(text, w) {
					t.Errorf("instructions lack %q:\n%s", w, text)
				}
			}
			if strings.Contains(text, "call den's validate_runbook") {
				t.Errorf("instructions send an agent without MCP tools to validate_runbook:\n%s", text)
			}
			if got := strings.Contains(text, "## The request\n\n"+opts.Request); got != (c.kind == Kimi) {
				t.Errorf("request in the instructions = %v, want it only for the agent without a first prompt", got)
			}
			if c.files != nil && c.kind != Kimi {
				if got := readFile(t, filepath.Join(dir, c.files[0])); got != text {
					t.Errorf("%s = %q, want the instructions", c.files[0], got)
				}
			}
		})
	}

	o.Files = t.TempDir()
	mustCommand(t, Agent{Kind: Kimi, Path: "kimi"}, o)
	dir := sessionDir(t, o.Files)
	agentFile := readFile(t, filepath.Join(dir, "kimi", "den-runbook.md"))
	if !strings.HasPrefix(agentFile, "---\nname: den-runbook\ndescription: ") || !strings.Contains(agentFile, "\n---\n${base_prompt}\n") ||
		!strings.Contains(agentFile, "read "+filepath.Join(dir, "kimi", "instructions.md")+" with your file tools") {
		t.Errorf("Kimi's agent file = %q, want front matter, the base prompt and the instructions' path", agentFile)
	}
	if got := readFile(t, filepath.Join(dir, "kimi", "instructions.md")); got != instructions(o, kinds[Kimi]) {
		t.Errorf("Kimi's instructions.md = %q", got)
	}

	o.Edit = true
	if text := instructions(o, kinds[Pi]); !strings.Contains(text, "changing the den runbook") || !strings.Contains(text, validate) {
		t.Errorf("edit instructions without MCP tools = %q", text)
	}
	o.Config = ""
	if got := o.validateCommand(); got != "/usr/local/bin/den runbook validate '/home/me/my runbooks/rds-status'" {
		t.Errorf("without a config = %q", got)
	}
}

func TestNote(t *testing.T) {
	if got := (Agent{Kind: Kimi}).Note("write it"); got != "Kimi Code takes no first prompt: type go in it to start" {
		t.Errorf("kimi = %q", got)
	}
	for _, a := range []Agent{{Kind: Kimi}, {Kind: Pi}, {Kind: Claude}, {Kind: "gemini"}} {
		request := "write it"
		if a.Kind == Kimi {
			request = ""
		}
		if got := a.Note(request); got != "" {
			t.Errorf("%s with %q = %q, want none", a.Kind, request, got)
		}
	}
	if (Agent{Kind: Pi}).MCP() || !(Agent{Kind: Copilot}).MCP() {
		t.Error("MCP() does not follow the kinds' table")
	}
}

func TestQuote(t *testing.T) {
	for _, c := range []struct{ goos, in, want string }{
		{"linux", "/usr/local/bin/den", "/usr/local/bin/den"},
		{"linux", "/home/me/it's mine", `'/home/me/it'\''s mine'`},
		{"darwin", "", "''"},
		{"windows", `C:\Program Files\den\den.exe`, `"C:\Program Files\den\den.exe"`},
		{"windows", "den", "den"},
	} {
		if got := quoteFor(c.goos, c.in); got != c.want {
			t.Errorf("quoteFor(%s, %q) = %s, want %s", c.goos, c.in, got, c.want)
		}
	}
}
