// Package agent starts an agent CLI — Claude Code, Codex and others — on a runbook's
// folder, to write the runbook from what the user asked for. den's runbook
// guide, and its MCP server to those that take one for a session, are handed
// over on the command line or in files den writes for that start, so the
// agent knows how a runbook looks and can check its work (with
// validate_runbook, or `den runbook validate` from its shell), and the user's
// own agent configuration is left as it is.
package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/prereq"
	"github.com/lukaszgard/den/internal/termsession"
)

// The agents den can start.
const (
	Claude   = "claude"
	Codex    = "codex"
	OpenCode = "opencode"
	Copilot  = "copilot"
	Qwen     = "qwen"
	Droid    = "droid"
	Pi       = "pi"
	Kimi     = "kimi"
	// Auto is the first of Kinds that is installed.
	Auto = "auto"
)

// Kinds are the agents in the order Auto tries them.
var Kinds = config.AgentKinds

// isKind says whether name is an agent CLI den knows how to start.
func isKind(name string) bool { return slices.Contains(Kinds, name) }

// kindSpec is how den hands one agent CLI its MCP server, instructions and request.
type kindSpec struct {
	title string // what messages call it: "Claude Code"
	// mcp: it takes den's MCP server for this session only. One that does not
	// checks its work with `den runbook validate` from its shell.
	mcp bool
	// prompt: it takes the request as the first prompt of its TUI. One that
	// does not reads the request with its instructions; the user starts it.
	prompt bool
	// args are den's arguments and environment, after the agent's own Args
	// (and "--").
	args func(s session) (args, env []string, err error)
}

// session is what a kindSpec writes its arguments from.
type session struct {
	o            Options
	server       []string // den's MCP server: "mcp", "-c", <config>
	instructions string   // instructions(o, spec)
	// files writes a setting file of this start and returns its absolute path.
	files func(name, content string) (string, error)
}

// kinds describes every entry of Kinds; TestEveryKindIsComplete holds the two
// together.
var kinds = map[string]kindSpec{
	Claude: {title: "Claude Code", mcp: true, prompt: true, args: func(s session) (args, env []string, err error) {
		mcpConfig := mustJSON(map[string]any{"mcpServers": map[string]any{"den": map[string]any{"command": s.o.Den, "args": s.server}}})
		// The prompt comes first: --mcp-config and --allowedTools take any
		// number of values and would swallow it.
		if s.o.Request != "" {
			args = append(args, s.o.Request)
		}
		args = append(args,
			"--mcp-config", mcpConfig,
			"--append-system-prompt", s.instructions,
			"--allowedTools", strings.Join(readOnlyTools, ","))
		return args, nil, nil
	}},
	Codex: {title: "Codex", mcp: true, prompt: true, args: func(s session) (args, env []string, err error) {
		// -c takes TOML values. A JSON string is a valid TOML basic string,
		// and a JSON array of them a valid TOML array.
		args = append(args,
			"-c", "mcp_servers.den.command="+mustJSON(s.o.Den),
			"-c", "mcp_servers.den.args="+mustJSON(s.server),
			"-c", "developer_instructions="+mustJSON(s.instructions))
		if s.o.Request != "" {
			args = append(args, s.o.Request)
		}
		return args, nil, nil
	}},
	// OpenCode merges the config OPENCODE_CONFIG names with the user's own
	// (ollama launch passes its own through OPENCODE_CONFIG_CONTENT, which is
	// merged too), and reads instructions from the files that config lists.
	OpenCode: {title: "OpenCode", mcp: true, prompt: true, args: func(s session) (args, env []string, err error) {
		instructions, err := s.files("opencode/instructions.md", s.instructions)
		if err != nil {
			return nil, nil, err
		}
		cfg, err := s.files("opencode/opencode.json", mustJSON(map[string]any{
			"$schema": "https://opencode.ai/config.json",
			"mcp": map[string]any{"den": map[string]any{
				"type": "local", "command": append([]string{s.o.Den}, s.server...), "enabled": true,
			}},
			"instructions": []string{instructions},
		}))
		if err != nil {
			return nil, nil, err
		}
		// Its positional argument is the project's path, not a prompt.
		if s.o.Request != "" {
			args = append(args, "--prompt", s.o.Request)
		}
		return args, []string{"OPENCODE_CONFIG=" + cfg}, nil
	}},
	// Copilot CLI adds --additional-mcp-config to the user's MCP servers for
	// this session, and reads AGENTS.md from the folders
	// COPILOT_CUSTOM_INSTRUCTIONS_DIRS lists, besides the repository's.
	Copilot: {title: "Copilot CLI", mcp: true, prompt: true, args: func(s session) (args, env []string, err error) {
		agentsMD, err := s.files("copilot/AGENTS.md", s.instructions)
		if err != nil {
			return nil, nil, err
		}
		dirs := filepath.Dir(agentsMD)
		if own := os.Getenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS"); own != "" {
			dirs += "," + own
		}
		args = append(args,
			"--additional-mcp-config", mustJSON(map[string]any{"mcpServers": map[string]any{"den": map[string]any{
				"type": "local", "command": s.o.Den, "args": s.server, "tools": []string{"*"},
			}}}),
			// --allow-tool takes any number of values: one each, with "=",
			// cannot take what follows for another.
			"--allow-tool=den(runbook_guide)", "--allow-tool=den(validate_runbook)")
		if s.o.Request != "" {
			args = append(args, "-i", s.o.Request)
		}
		return args, []string{"COPILOT_CUSTOM_INSTRUCTIONS_DIRS=" + dirs}, nil
	}},

	// The agents below take no MCP server for one session without changing
	// the user's config: they check their work with den runbook validate.
	Qwen: {title: "Qwen Code", prompt: true, args: func(s session) (args, env []string, err error) {
		args = append(args, "--append-system-prompt", s.instructions)
		if s.o.Request != "" {
			args = append(args, "-i", s.o.Request)
		}
		return args, nil, nil
	}},
	Droid: {title: "Droid", prompt: true, args: func(s session) (args, env []string, err error) {
		path, err := s.files("droid/instructions.md", s.instructions)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--append-system-prompt-file", path)
		if s.o.Request != "" {
			args = append(args, s.o.Request)
		}
		return args, nil, nil
	}},
	// Pi's --append-system-prompt takes text or a file's path: the path keeps
	// the guide off its command line.
	Pi: {title: "Pi", prompt: true, args: func(s session) (args, env []string, err error) {
		path, err := s.files("pi/instructions.md", s.instructions)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--append-system-prompt", path)
		if s.o.Request != "" {
			args = append(args, s.o.Request)
		}
		return args, nil, nil
	}},
	// Kimi Code takes no first prompt for its TUI, and adds to its system
	// prompt only through an agent file, whose body it renders as a template:
	// the guide, with its ${…} examples, stays in a file of its own that the
	// agent is told to read.
	Kimi: {title: "Kimi Code", args: func(s session) (args, env []string, err error) {
		instructions, err := s.files("kimi/instructions.md", s.instructions)
		if err != nil {
			return nil, nil, err
		}
		path, err := s.files("kimi/den-runbook.md", "---\n"+
			"name: den-runbook\n"+
			"description: Writes or changes the den runbook in the working directory.\n"+
			"---\n"+
			"${base_prompt}\n\n"+
			"Before anything else, read "+instructions+" with your file tools: it holds den's runbook guide and your task. Follow it.\n")
		if err != nil {
			return nil, nil, err
		}
		return append(args, "--agent-file", path), nil, nil
	}},
}

// MCP says whether the agent takes den's MCP server; one that does not checks
// its work with `den runbook validate`.
func (a Agent) MCP() bool { return kinds[a.Kind].mcp }

// Note is what the user must do to start an agent that takes no first
// prompt, which reads request with its instructions; "" for any other.
func (a Agent) Note(request string) string {
	spec, ok := kinds[a.Kind]
	if !ok || spec.prompt || request == "" {
		return ""
	}
	return spec.title + " takes no first prompt: type go in it to start"
}

// kindTitles are the agents' titles in the order Auto tries them.
func kindTitles() []string {
	titles := make([]string, len(Kinds))
	for i, k := range Kinds {
		titles[i] = kinds[k].title
	}
	return titles
}

// The read-only den tools an agent may call without asking: they read the
// guide and check a folder, nothing more.
var readOnlyTools = []string{"mcp__den__runbook_guide", "mcp__den__validate_runbook"}

// Find picks the agent to start: preferred, or for Auto (and "") the first of
// Kinds that is installed. It is found the way den doctor finds tools.
func Find(preferred string) (kind, path string, err error) {
	return find(preferred, nil)
}

// find is Find, naming the entries of runbooks.agents too in the error for a
// name that is none of the built-ins.
func find(preferred string, specs []config.AgentSpec) (kind, path string, err error) {
	switch preferred {
	case "", Auto:
		for _, k := range Kinds {
			if p, ok := prereq.Find(k); ok {
				return k, p, nil
			}
		}
		return "", "", fmt.Errorf("no agent found: install one of %s (den doctor --for %s shows how)",
			strings.Join(kindTitles(), ", "), Kinds[0])
	}
	if isKind(preferred) {
		if p, ok := prereq.Find(preferred); ok {
			return preferred, p, nil
		}
		return "", "", fmt.Errorf("%s not found — %s", preferred, prereq.InstallHint(preferred))
	}
	if len(specs) > 0 {
		names := make([]string, len(specs))
		for i, s := range specs {
			names[i] = s.Name
		}
		return "", "", fmt.Errorf("%q is not an agent den can start (auto, %s or a runbooks.agents name: %s)",
			preferred, strings.Join(Kinds, ", "), strings.Join(names, ", "))
	}
	return "", "", fmt.Errorf("%q is not an agent den can start (auto or %s)", preferred, strings.Join(Kinds, ", "))
}

// Agent is an agent den can start: the program, the arguments that come
// before den's own, and which CLI it is, since that decides how den's
// arguments are written.
type Agent struct {
	Name string // what the pop-up shows and --agent names
	Kind string // one of Kinds
	Path string // the program, found
	Args []string
	// Separator puts "--" between Args and den's arguments. A wrapper such as
	// `ollama launch claude` reads its own flags up to it and hands the rest to
	// the CLI; without it the wrapper would take den's flags for its own.
	Separator bool
	// Typed marks an agent that a runbook gave as a command line: its Name is
	// that command, which Pick resolves again.
	Typed bool
}

// Resolve turns a runbooks.agents entry into an Agent: the command is split on
// whitespace, the kind is the entry's or inferred from the command, and the
// program is looked for the way Find does for a kind, else on PATH.
// The error says what is missing.
func Resolve(spec config.AgentSpec) (Agent, error) {
	words := strings.Fields(spec.Command)
	if len(words) == 0 {
		return Agent{}, fmt.Errorf("agent %q has no command", spec.Name)
	}
	kind := spec.Kind
	if kind == "" {
		kind = config.AgentKind(spec.Command)
	}
	if !isKind(kind) {
		return Agent{}, fmt.Errorf("agent %q: cannot tell which agent CLI %q starts, set kind", spec.Name, spec.Command)
	}
	prog := programName(words[0])
	path, err := findProgram(words[0])
	if err != nil {
		return Agent{}, fmt.Errorf("agent %q: %w", spec.Name, err)
	}
	return Agent{
		Name: spec.Name, Kind: kind, Path: path,
		Args: words[1:],
		// Only `ollama launch` reads flags up to a "--"; `ollama run` and the like
		// do not take it.
		Separator: prog == "ollama" && len(words) > 1 && words[1] == "launch",
	}, nil
}

// programName is a command word's file name without folder or .exe, in lower
// case; both slash styles count so a Windows path reads the same everywhere.
func programName(word string) string {
	word = strings.ReplaceAll(word, `\`, "/")
	word = word[strings.LastIndex(word, "/")+1:]
	return strings.TrimSuffix(strings.ToLower(word), ".exe")
}

func findProgram(typed string) (string, error) {
	word := platform.ExpandHome(typed)
	switch {
	case filepath.IsAbs(word):
		if _, err := os.Stat(word); err != nil {
			return "", fmt.Errorf("%s not found", typed)
		}
		return word, nil
	case isKind(word):
		if p, ok := prereq.Find(word); ok {
			return p, nil
		}
		return "", fmt.Errorf("%s not found — %s", word, prereq.InstallHint(word))
	}
	p, err := exec.LookPath(word)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH", typed)
	}
	// The agent starts in the runbook's folder, where a relative path such as
	// ./ai-claude would be read afresh and not be found: it is fixed against
	// the directory den runs in, as LookPath read it.
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("%s: %w", typed, err)
	}
	return abs, nil
}

// Choices are the agents a pop-up offers, in the order of runbooks.agents, and
// the index of the one to start unless the user picks: preferred's (an entry
// name), else the first. Entries that do not resolve are left out, so one
// missing program does not hide the others; it is an error only when none
// resolves. Without entries it is Find(preferred), named after its kind.
// A preferred kind that is no entry's name is the built-in agent,
// added last and made the default when it is installed, so the pop-up starts
// what Select, and so the command line, would.
func Choices(specs []config.AgentSpec, preferred string) ([]Agent, int, error) {
	if len(specs) == 0 {
		kind, path, err := Find(preferred)
		if err != nil {
			return nil, 0, err
		}
		return []Agent{{Name: kind, Kind: kind, Path: path}}, 0, nil
	}
	var agents []Agent
	var missing []error
	def := 0
	for _, s := range specs {
		a, err := Resolve(s)
		if err != nil {
			missing = append(missing, err)
			continue
		}
		if s.Name == preferred {
			def = len(agents)
		}
		agents = append(agents, a)
	}
	if isKind(preferred) {
		if !slices.ContainsFunc(specs, func(s config.AgentSpec) bool { return s.Name == preferred }) {
			if kind, path, err := Find(preferred); err == nil {
				def = len(agents)
				agents = append(agents, Agent{Name: kind, Kind: kind, Path: path})
			}
		}
	}
	if len(agents) == 0 {
		return nil, 0, fmt.Errorf("no agent in runbooks.agents can start: %w", errors.Join(missing...))
	}
	return agents, def, nil
}

// Select is the agent a name asks for, as --agent and runbooks.agent spell it.
// An entry of runbooks.agents wins over the kinds, which it may shadow,
// and must resolve: the user asked for that one. Otherwise it is Choices'
// default, where "" and auto mean the first entry that resolves.
func Select(specs []config.AgentSpec, name string) (Agent, error) {
	for _, s := range specs {
		if s.Name == name {
			return Resolve(s)
		}
	}
	if len(specs) > 0 && (name == "" || name == Auto) {
		agents, def, err := Choices(specs, "")
		if err != nil {
			return Agent{}, err
		}
		return agents[def], nil
	}
	kind, path, err := find(name, specs)
	if err != nil {
		return Agent{}, err
	}
	return Agent{Name: kind, Kind: kind, Path: path}, nil
}

// Pick is the agent a runbook's agent setting asks for: a name Select knows
// (auto, a kind, a runbooks.agents entry), else a command line such as
// `claude --model opus`, read the way a runbooks.agents command is. A command
// keeps itself as its Name, so Pick(specs, a.Name) starts it again.
func Pick(specs []config.AgentSpec, value string) (Agent, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == Auto || isKind(value) || slices.ContainsFunc(specs, func(s config.AgentSpec) bool { return s.Name == value }) {
		return Select(specs, value)
	}
	a, err := Resolve(config.AgentSpec{Name: value, Command: value})
	a.Typed = true
	return a, err
}

// Label is the agent's name as a tab or a hint shows it: a command line a
// runbook typed is cut to its kind and model, `claude opus` for `claude
// --model opus`; a name stands as it is.
func (a Agent) Label() string {
	if !a.Typed {
		return a.Name
	}
	return typedLabel(a.Name)
}

// Describe is what Pick(specs, value) would be called, without looking for
// the program: a hint can show it on every redraw. An unset or auto value is
// just "agent", since which one starts depends on what is installed.
func Describe(specs []config.AgentSpec, value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "" || value == Auto:
		return "agent"
	case isKind(value) || slices.ContainsFunc(specs, func(s config.AgentSpec) bool { return s.Name == value }):
		return value
	}
	return typedLabel(value)
}

// typedLabel is a command line's kind and its --model, "claude opus".
func typedLabel(command string) string {
	words := strings.Fields(command)
	label := config.AgentKind(command)
	if label == "" && len(words) > 0 {
		label = programName(words[0])
	}
	for i, w := range words {
		if model, ok := strings.CutPrefix(w, "--model="); ok {
			return label + " " + model
		}
		if (w == "--model" || w == "-m") && i+1 < len(words) {
			return label + " " + words[i+1]
		}
	}
	return label
}

// Options are what the agent is started with.
type Options struct {
	Den     string // den's own executable, which the agent starts as its MCP server
	Config  string // den.yaml, absolute, so that server reads the same config
	Dir     string // the runbook's folder: the agent's working directory
	Guide   string // den's runbook guide for this den (runbook.Registry.Guide)
	Request string // what the user asked for; empty starts the agent without a prompt
	// Edit says the runbook in Dir exists and is to be changed, not written.
	Edit bool
	// Files is the directory den keeps the files some agents read their
	// settings from (OpenCode's config, Copilot's AGENTS.md, Kimi's agent
	// file); empty is platform.StateDir()/agents. Tests point it at a temp dir.
	Files string
}

// Instructions are what an agent with den's MCP server is told on top of its
// own instructions.
func (o Options) Instructions() string {
	return o.task("call den's validate_runbook with this folder until it reports ok")
}

// task is the guide and the task, checked the way check says.
func (o Options) task(check string) string {
	if o.Edit {
		return strings.TrimSpace(o.Guide) + "\n\n## Your task\n\n" +
			"You are changing the den runbook in " + o.Dir + ", your working directory, as the user asks. " +
			"Read its runbook.yaml, script and README.md first, keep what works, and change only what the request needs. " +
			"When you are done, " + check + ", " +
			"then tell the user what changed and how to run it."
	}
	return strings.TrimSpace(o.Guide) + "\n\n## Your task\n\n" +
		"You are writing the den runbook in " + o.Dir + ", your working directory. den created it with a " +
		"runbook.yaml, run.sh and README.md to start from: change or replace them as the request needs " +
		"(remove run.sh when the runbook is not a shell script). When it is written, " + check +
		", then tell the user how to run it."
}

// validateCommand is the shell command that checks the runbook in o.Dir, for
// an agent without den's MCP tools.
func (o Options) validateCommand() string {
	cmd := quote(o.Den) + " runbook validate"
	if o.Config != "" {
		cmd += " -c " + quote(o.Config)
	}
	return cmd + " " + quote(o.Dir)
}

// quote is a word of a shell command line an agent runs, quoted when it needs
// it: in POSIX single quotes, or on Windows in double quotes, which cmd and
// PowerShell both read and a Windows path cannot contain.
func quote(s string) string { return quoteFor(runtime.GOOS, s) }

func quoteFor(goos, s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\|&;<>()*?[]#~!{}%") {
		return s
	}
	if goos == "windows" {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// instructions are what an agent of spec is told: Instructions, for one that
// takes den's MCP server; for one that does not, the same with
// `den runbook validate` in validate_runbook's place, which the guide names.
// An agent that takes no first prompt reads the request here.
func instructions(o Options, spec kindSpec) string {
	text := o.Instructions()
	if !spec.mcp {
		text = o.task("run `"+o.validateCommand()+"` in your shell until it reports the runbook ready") +
			"\n\nYou have no den MCP tools in this session: wherever the guide says validate_runbook, " +
			"run that command; run_runbook and den's other tools are not there."
	}
	if !spec.prompt && o.Request != "" {
		text += "\n\n## The request\n\n" + o.Request +
			"\n\nThe user starts the session with a short message such as \"go\": begin on this request then."
	}
	return text
}

// Command is the agent's command line: interactive, in the runbook's folder.
// The agent's own arguments come first, then "--" when it needs one, then den's.
// An agent that reads its settings from files has them written first, under
// o.Files.
func Command(a Agent, o Options) (termsession.Command, error) {
	spec, ok := kinds[a.Kind]
	if !ok {
		return termsession.Command{}, fmt.Errorf("agent %q: no way to start kind %q", a.Name, a.Kind)
	}
	server := []string{"mcp"}
	if o.Config != "" {
		server = append(server, "-c", o.Config)
	}
	args := append([]string(nil), a.Args...)
	if a.Separator {
		args = append(args, "--")
	}
	text := instructions(o, spec)
	files := newSessionFiles(o.Files, a.Kind, o.Den, o.Config, o.Dir, o.Request, text)
	more, env, err := spec.args(session{o: o, server: server, instructions: text, files: files.write})
	files.done(time.Now())
	if err != nil {
		return termsession.Command{}, fmt.Errorf("agent %q: %w", a.Name, err)
	}
	return termsession.Command{Path: a.Path, Args: append(args, more...), Env: env, Dir: o.Dir}, nil
}

// mustJSON encodes v without escaping <, > and &, which TOML would keep as
// the escapes; strings and string slices cannot fail to encode.
func mustJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
