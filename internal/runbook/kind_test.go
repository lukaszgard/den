package runbook

import (
	"path/filepath"
	"testing"
)

func TestKind(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string { return writeScript(t, dir, name, body, true) }
	write("run.sh", "#!/usr/bin/env bash\necho\n")
	write("main.py", "#!/usr/bin/env -S python3.12 -u\n")
	write("tool", "#!/bin/zsh\n")
	write("noshebang.rb", "puts 1\n")
	write("index.ts", "console.log(1)\n")

	cases := []struct {
		name string
		rb   Runbook
		want string
	}{
		{"folder, shebang via env", Runbook{Dir: dir, Cwd: dir, Command: "./run.sh"}, "bash"},
		{"folder, env -S with a version", Runbook{Dir: dir, Cwd: dir, Command: "python3 main.py --fast"}, "python"},
		{"folder, absolute shebang", Runbook{Dir: dir, Cwd: dir, Command: "./tool"}, "zsh"},
		{"folder, extension only", Runbook{Dir: dir, Cwd: dir, Command: "ruby noshebang.rb"}, "ruby"},
		{"folder, program without a script", Runbook{Dir: dir, Cwd: dir, Command: "go run ./cmd/x"}, "go"},
		{"folder, node runner", Runbook{Dir: dir, Cwd: dir, Command: "npx tsx index.ts"}, "node"},
		{"header script", Runbook{Source: filepath.Join(dir, "run.sh"), Command: "'" + filepath.Join(dir, "run.sh") + "'"}, "bash"},
		{"inline aws", Runbook{Command: "aws rds describe-db-clusters \\\n  --output table"}, "aws"},
		{"inline with an assignment", Runbook{Command: "AWS_PAGER= kubectl get pods"}, "kubectl"},
		{"inline one-liner", Runbook{Command: "echo hello"}, "command"},
		{"inline script", Runbook{Command: "set -e\necho one\necho two"}, "shell"},
		{"inline make", Runbook{Command: "make deploy ENV=int"}, "make"},
		{"rust through cargo", Runbook{Dir: dir, Cwd: dir, Command: "cargo run --quiet --release"}, "rust"},
	}
	for _, c := range cases {
		if got := kindOf(c.rb); got != c.want {
			t.Errorf("%s: kindOf = %q, want %q", c.name, got, c.want)
		}
	}
}
