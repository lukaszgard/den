package runbook

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// interpreters maps a program to the kind of runbook it runs.
var interpreters = map[string]string{
	"bash": "bash", "sh": "sh", "zsh": "zsh", "dash": "sh", "ksh": "sh", "fish": "fish",
	"python": "python", "python3": "python", "uv": "python", "poetry": "python", "pipenv": "python",
	"node": "node", "npm": "node", "npx": "node", "yarn": "node", "pnpm": "node", "bun": "node",
	"deno": "node", "tsx": "node", "ts-node": "node",
	"go": "go", "cargo": "rust", "rustc": "rust", "rust-script": "rust",
	"ruby": "ruby", "bundle": "ruby", "perl": "perl",
	"java": "java", "mvn": "java", "gradle": "java", "pwsh": "pwsh",
	"make": "make", "just": "make", "docker": "docker", "kubectl": "kubectl", "helm": "kubectl",
	"terraform": "terraform", "tofu": "terraform", "aws": "aws",
}

// extensions maps a script's extension to its kind when it has no shebang.
var extensions = map[string]string{
	".sh": "bash", ".bash": "bash", ".zsh": "zsh", ".py": "python", ".go": "go",
	".js": "node", ".mjs": "node", ".cjs": "node", ".ts": "node", ".rb": "ruby",
	".rs": "rust", ".java": "java", ".pl": "perl", ".ps1": "pwsh",
}

// versioned matches an interpreter with its version: python3.12, ruby3.
var versioned = regexp.MustCompile(`^([a-z]+?)[0-9.]+$`)

// kindOf names what a runbook runs, for Runbook.Kind: the interpreter of its
// script (its shebang, else its extension), else the program its command
// starts, else "command" for a one-line command and "shell" for a multi-line
// one. It reads the script, so it runs once per definition, not per redraw.
func kindOf(rb Runbook) string {
	script := rb.Source
	if rb.Dir != "" {
		script = entryScript(rb)
	}
	if script != "" {
		if kind := scriptKind(script); kind != "" {
			return kind
		}
	}
	command := strings.TrimSpace(rb.Command)
	first, rest, _ := strings.Cut(command, "\n")
	if kind := commandKind(first); kind != "" {
		return kind
	}
	if strings.TrimSpace(rest) != "" {
		return "shell"
	}
	return "command"
}

func scriptKind(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	if shebang, ok := strings.CutPrefix(line, "#!"); ok {
		fields := strings.Fields(shebang)
		// #!/usr/bin/env [-S] python3 names the interpreter after env.
		for len(fields) > 0 && (filepath.Base(fields[0]) == "env" || strings.HasPrefix(fields[0], "-")) {
			fields = fields[1:]
		}
		if len(fields) > 0 {
			if kind := program(fields[0]); kind != "" {
				return kind
			}
		}
	}
	return extensions[strings.ToLower(filepath.Ext(path))]
}

// commandKind is the kind of the program a command line starts, skipping
// leading VAR=value assignments, and of a script it names by extension.
func commandKind(line string) string {
	for _, word := range strings.Fields(line) {
		word = strings.Trim(word, `"'`)
		if strings.Contains(word, "=") && !strings.HasPrefix(word, "=") {
			continue
		}
		if kind := program(word); kind != "" {
			return kind
		}
		return extensions[strings.ToLower(filepath.Ext(word))]
	}
	return ""
}

func program(path string) string {
	name := filepath.Base(path)
	if kind, ok := interpreters[name]; ok {
		return kind
	}
	if m := versioned.FindStringSubmatch(name); m != nil {
		return interpreters[m[1]]
	}
	return ""
}
