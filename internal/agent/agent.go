// Package agent starts an agent CLI — Claude Code or Codex — on a runbook's
// folder, to write the runbook from what the user asked for. den's MCP server
// and its runbook guide are handed over on the command line, so the agent
// knows how a runbook looks and can check its work with validate_runbook,
// and the user's own agent configuration is left as it is.
package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/platform"
	"github.com/lukaszgard/den/internal/prereq"
	"github.com/lukaszgard/den/internal/termsession"
)

// The agents den can start.
const (
	Claude = "claude"
	Codex  = "codex"
	// Auto is the first of Kinds that is installed.
	Auto = "auto"
)

// Kinds are the agents in the order Auto tries them.
var Kinds = []string{Claude, Codex}

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
		return "", "", fmt.Errorf("no agent found: install Claude Code (%s) or Codex (%s)",
			prereq.InstallHint(Claude), prereq.InstallHint(Codex))
	case Claude, Codex:
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
		return "", "", fmt.Errorf("%q is not an agent den can start (auto, claude, codex or a runbooks.agents name: %s)",
			preferred, strings.Join(names, ", "))
	}
	return "", "", fmt.Errorf("%q is not an agent den can start (auto, claude or codex)", preferred)
}

// Agent is an agent den can start: the program, the arguments that come
// before den's own, and which CLI it is, since that decides how den's
// arguments are written.
type Agent struct {
	Name string // what the pop-up shows and --agent names
	Kind string // Claude or Codex
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
// program is looked for the way Find does for claude and codex, else on PATH.
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
	if kind != Claude && kind != Codex {
		return Agent{}, fmt.Errorf("agent %q: cannot tell whether %q starts claude or codex, set kind", spec.Name, spec.Command)
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
	case word == Claude || word == Codex:
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
// A preferred claude or codex that is no entry's name is the built-in agent,
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
	if preferred == Claude || preferred == Codex {
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
// An entry of runbooks.agents wins over claude and codex, which it may shadow,
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
// (auto, claude, codex, a runbooks.agents entry), else a command line such as
// `claude --model opus`, read the way a runbooks.agents command is. A command
// keeps itself as its Name, so Pick(specs, a.Name) starts it again.
func Pick(specs []config.AgentSpec, value string) (Agent, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == Auto || value == Claude || value == Codex || slices.ContainsFunc(specs, func(s config.AgentSpec) bool { return s.Name == value }) {
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
	case value == Claude || value == Codex || slices.ContainsFunc(specs, func(s config.AgentSpec) bool { return s.Name == value }):
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
}

// Instructions are what the agent is told on top of its own instructions.
func (o Options) Instructions() string {
	if o.Edit {
		return strings.TrimSpace(o.Guide) + "\n\n## Your task\n\n" +
			"You are changing the den runbook in " + o.Dir + ", your working directory, as the user asks. " +
			"Read its runbook.yaml, script and README.md first, keep what works, and change only what the request needs. " +
			"When you are done, call den's validate_runbook with this folder until it reports ok, " +
			"then tell the user what changed and how to run it."
	}
	return strings.TrimSpace(o.Guide) + "\n\n## Your task\n\n" +
		"You are writing the den runbook in " + o.Dir + ", your working directory. den created it with a " +
		"runbook.yaml, run.sh and README.md to start from: change or replace them as the request needs " +
		"(remove run.sh when the runbook is not a shell script). When it is written, call den's " +
		"validate_runbook with this folder until it reports ok, then tell the user how to run it."
}

// Command is the agent's command line: interactive, in the runbook's folder.
// The agent's own arguments come first, then "--" when it needs one, then den's.
func Command(a Agent, o Options) termsession.Command {
	server := []string{"mcp"}
	if o.Config != "" {
		server = append(server, "-c", o.Config)
	}
	args := append([]string(nil), a.Args...)
	if a.Separator {
		args = append(args, "--")
	}
	switch a.Kind {
	case Claude:
		mcpConfig := mustJSON(map[string]any{"mcpServers": map[string]any{"den": map[string]any{"command": o.Den, "args": server}}})
		// The prompt comes first: --mcp-config and --allowedTools take any
		// number of values and would swallow it.
		if o.Request != "" {
			args = append(args, o.Request)
		}
		args = append(args,
			"--mcp-config", mcpConfig,
			"--append-system-prompt", o.Instructions(),
			"--allowedTools", strings.Join(readOnlyTools, ","))
	case Codex:
		// -c takes TOML values. A JSON string is a valid TOML basic string,
		// and a JSON array of them a valid TOML array.
		args = append(args,
			"-c", "mcp_servers.den.command="+mustJSON(o.Den),
			"-c", "mcp_servers.den.args="+mustJSON(server),
			"-c", "developer_instructions="+mustJSON(o.Instructions()))
		if o.Request != "" {
			args = append(args, o.Request)
		}
	}
	return termsession.Command{Path: a.Path, Args: args, Dir: o.Dir}
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
