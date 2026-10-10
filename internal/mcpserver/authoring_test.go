package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lukaszgard/den/internal/config"
)

func authoringSetup(t *testing.T) (*setup, string) {
	t.Helper()
	root := t.TempDir()
	return &setup{
		dirs: []string{root},
		envs: map[string]config.EnvSpec{"eu-staging": {AWSProfile: "app-e2e", AWSRegionCode: "eu-central-1", Environment: "e2e"}},
	}, root
}

// An agent can learn how to write a runbook and check one on any config: the
// tools only read, and give out no credential.
func TestAuthoringToolsAreThereWithoutAllowRunbooks(t *testing.T) {
	s, root := authoringSetup(t)
	cs := start(t, s)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	got := strings.Join(names, " ")
	if !strings.Contains(got, "runbook_guide") || !strings.Contains(got, "validate_runbook") || strings.Contains(got, "run_runbook") {
		t.Fatalf("tools = %s, want the authoring tools and not run_runbook", got)
	}

	out, isErr := call(t, cs, "runbook_guide", map[string]any{})
	if isErr || !strings.Contains(out, "Writing a den runbook") || !strings.Contains(out, "app-e2e") || !strings.Contains(out, root) {
		t.Fatalf("runbook_guide = %v %.300s", isErr, out)
	}
	if out, isErr := call(t, cs, "runbook_guide", map[string]any{"folder": "/etc"}); !isErr {
		t.Fatalf("a folder outside the runbooks directories was accepted: %.200s", out)
	}
}

func TestValidateRunbookChecksTheFolderAndRescans(t *testing.T) {
	s, root := authoringSetup(t)
	s.policy.AllowRunbooks = true
	cs := start(t, s)
	dir := filepath.Join(root, "rds-status")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	write("runbook.yaml", "name: RDS status\ncommand: ./run.sh\nenv: eu-staging\ntimeot: 1m\n")
	out, isErr := call(t, cs, "validate_runbook", map[string]any{"folder": "rds-status"})
	if isErr || !strings.Contains(out, `"ok":false`) || !strings.Contains(out, "timeot") {
		t.Fatalf("a misspelt key should fail validation: %s", out)
	}

	write("runbook.yaml", "name: RDS status\ndescription: d\ncommand: ./run.sh\nenv: eu-staging\ntimeout: 1m\n")
	write("run.sh", "#!/bin/sh\necho ok\n")
	write("README.md", "# RDS status\n")
	out, isErr = call(t, cs, "validate_runbook", map[string]any{"folder": dir})
	if isErr || !strings.Contains(out, `"ok":true`) || !strings.Contains(out, `"kind":"sh"`) {
		t.Fatalf("a good folder: %s", out)
	}
	if out, _ := call(t, cs, "list_runbooks", nil); !strings.Contains(out, "RDS status") {
		t.Fatalf("after a good validation den should know the runbook: %s", out)
	}
	if out, isErr := call(t, cs, "validate_runbook", map[string]any{"folder": "../elsewhere"}); !isErr {
		t.Fatalf("a path outside the runbooks directories was accepted: %s", out)
	}
}

func TestNewRunbookPrompt(t *testing.T) {
	s, _ := authoringSetup(t)
	cs := start(t, s)
	res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{
		Name:      "new_runbook",
		Arguments: map[string]string{"request": "check the status of my RDS instances in eu-staging, in Rust", "name": "RDS status"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{"Writing a den runbook", "app-e2e", "in eu-staging, in Rust", `Name it "RDS status"`, "validate_runbook"} {
		if !strings.Contains(text, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if _, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "new_runbook", Arguments: map[string]string{}}); err == nil {
		t.Error("the prompt without a request should be refused")
	}
}
