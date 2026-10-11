package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/doctor"
	"github.com/lukaszgard/den/internal/prereq"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/kafka"
)

// These run the real command tree in-process, against a temp den.yaml whose
// AWS config is an empty temp file, so they never touch the developer's own.

func tempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	aws := filepath.Join(dir, "aws-config")
	if err := os.WriteFile(aws, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `version: "1"
aws:
  config_path: ` + aws + `
  credentials_path: ` + aws + `
services:
  - name: orders
    type: rds
    rds:
      region: lab
      environment: int
      ec2_instance_id: i-0abc
      aws_profile: dev
      aws_region_code: eu-west-1
      rds_host: orders.cluster.rds.amazonaws.com
      db_user: app
      local_port: 50200
tunnels:
  - name: bastion
    command: ssh -N -D 5555 bastion.example.com
runbooks:
  items:
    - name: hello
      command: echo hi
      params:
        - name: who
          required: true
`
	path := filepath.Join(dir, "den.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runDen(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	cfgFile = "" // the flag binds a package variable; do not leak it between runs
	return out.String(), err
}

func TestListJSON(t *testing.T) {
	out, err := runDen(t, "list", "--json", "-c", tempConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	var got []service.Summary
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 || got[0].Name != "orders" || got[0].LocalPort != 50200 || got[0].Status != "idle" || got[1].Type != "tunnel" {
		t.Fatalf("summaries = %+v", got)
	}
	if !strings.Contains(got[0].ConnectCommand, "port=50200") {
		t.Fatalf("connect command = %q", got[0].ConnectCommand)
	}
}

// A broken den.yaml stops a command with every error in it, each on a line
// of its own that names the file and the line.
func TestBrokenConfigListsEveryErrorWithItsLine(t *testing.T) {
	path := tempConfig(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), "    command: ssh -N -D 5555 bastion.example.com\n", "", 1) +
		"vpn:\n  - name: office\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = runDen(t, "list", "-c", path)
	if err == nil {
		t.Fatal("den list ran with a broken config")
	}
	for _, want := range []string{
		"invalid config:",
		path + `:18: tunnel[0] "bastion": command is required`,
		path + `:27: vpn[0] "office": gateway or config_file is required`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestConfigJSONUsesYAMLKeys(t *testing.T) {
	// The temp config has warnings, which go to the error stream: stdout stays JSON.
	cmd := newRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"config", "--json", "-c", tempConfig(t)})
	err := cmd.Execute()
	cfgFile = ""
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(stderr.String(), "AWS profile") {
		t.Errorf("the warnings should be on the error stream:\n%s", stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	services, _ := got["services"].([]any)
	first, _ := services[0].(map[string]any)
	if first["name"] != "orders" || first["rds"] == nil {
		t.Fatalf("config JSON should use den.yaml's keys: %v", first)
	}
}

func TestRunListJSON(t *testing.T) {
	out, err := runDen(t, "run", "--list", "--json", "-c", tempConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	var got []runbookEntry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0].Name != "hello" || len(got[0].Params) != 1 || !got[0].Params[0].Required {
		t.Fatalf("runbooks = %+v", got)
	}
}

func TestSchemaIsJSONSchema(t *testing.T) {
	out, err := runDen(t, "schema")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["$schema"] == nil {
		t.Fatalf("den schema did not print a JSON Schema: %v", err)
	}
}

func TestDoctorJSON(t *testing.T) {
	out, err := runDen(t, "doctor", "--json", "-c", tempConfig(t))
	var got []doctor.Result
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(got) == 0 || got[0].Check != "config" || got[0].Status != doctor.OK {
		t.Fatalf("results = %+v", got)
	}
	// The temp AWS config defines no profiles: the config check says so, once,
	// as a warning with the hint as its fix.
	profiles := 0
	for _, r := range got {
		if r.Check == "config" && strings.Contains(r.Detail, `AWS profile "dev"`) {
			profiles++
			if r.Status != doctor.Warn || !strings.Contains(r.Fix, "aws configure sso --profile dev") {
				t.Errorf("profile result = %+v", r)
			}
		}
	}
	if profiles != 1 {
		t.Errorf("the missing profile is reported %d times, want once:\n%+v", profiles, got)
	}
	if err != nil {
		if _, ok := errors.AsType[exitCodeError](err); !ok {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestMissingExplicitConfigIsAnError(t *testing.T) {
	if _, err := runDen(t, "list", "-c", filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("-c naming a missing file must fail")
	}
}

func TestConnectNeedsAConfiguredService(t *testing.T) {
	cfg := tempConfig(t)
	if _, err := runDen(t, "connect", "nope", "-c", cfg); err == nil || !strings.Contains(err.Error(), `no service "nope"`) || !strings.Contains(err.Error(), "orders") {
		t.Fatalf("err = %v, want the unknown name and the names there are", err)
	}
	if _, err := runDen(t, "connect", "-c", cfg); err == nil {
		t.Fatal("connect without a name must fail")
	}
}

func TestConnectKafkaSaysPlanned(t *testing.T) {
	path := tempConfig(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("tunnels:"), []byte(`  - name: events
    type: kafka
    kafka:
      ec2_instance_id: i-0123456789abcdef0
      aws_region_code: eu-west-1
      kafka_host: broker.example.com
tunnels:`), 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runDen(t, "connect", "events", "-c", path); !errors.Is(err, kafka.ErrPlanned) {
		t.Fatalf("err = %v, want kafka.ErrPlanned", err)
	}
}

// --explain must show the redacted report, send only that, and restore the
// real names in the answer. The API is a local fake via ANTHROPIC_BASE_URL.
func TestDoctorExplain(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		sent = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5",
			"content":[{"type":"text","text":"Define profile-1 in your AWS config: service-1 needs it."}],
			"stop_reason":"end_turn","stop_details":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "test")

	out, err := runDen(t, "doctor", "--explain", "--yes", "-c", tempConfig(t))
	if _, ok := errors.AsType[exitCodeError](err); err != nil && !ok {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if strings.Contains(sent, `\"dev\"`) || strings.Contains(sent, "orders") || strings.Contains(sent, "i-0abc") {
		t.Fatalf("the request carried real names:\n%s", sent)
	}
	if !strings.Contains(sent, "profile-1") {
		t.Fatalf("the report was not sent redacted:\n%s", sent)
	}
	if !strings.Contains(out, "This is exactly what would be sent") || !strings.Contains(out, "Define dev in your AWS config: orders needs it.") {
		t.Fatalf("output should show the payload and the restored answer:\n%s", out)
	}
}

// check-install checks only what the named features need, on a PATH holding
// just an old sops, and says how to install or upgrade on this OS.
func TestCheckInstallFor(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "sops"), []byte("#!/bin/sh\necho 'sops 3.8.1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	out, err := runDen(t, "check-install", "--for", "sops,vault", "--json")
	if _, ok := errors.AsType[exitCodeError](err); !ok {
		t.Fatalf("a missing tool asked for by name must fail: %v\n%s", err, out)
	}
	var results []doctor.Result
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(results) != 2 {
		t.Fatalf("--for sops,vault checked %d things: %+v", len(results), results)
	}
	sops, vault := results[0], results[1]
	if sops.Check != "sops" || sops.Status != doctor.Warn || !strings.Contains(sops.Detail, "3.8.1") || len(sops.Install) == 0 {
		t.Errorf("sops 3.8 decrypts but cannot edit, and should say how to upgrade: %+v", sops)
	}
	if vault.Check != "vault" || vault.Status != doctor.Fail || len(vault.Install) == 0 || vault.Docs == "" {
		t.Errorf("missing vault: %+v", vault)
	}

	if _, err := runDen(t, "doctor", "--for", "sops,kafka"); err == nil || !strings.Contains(err.Error(), "choose from") {
		t.Errorf("an unknown feature must list the valid ones: %v", err)
	}
}

func TestDoctorPreviewsAnotherPlatform(t *testing.T) {
	out, err := runDen(t, "doctor", "--for", "sops", "--os", "debian/amd64")
	if err != nil || !strings.Contains(out, "_amd64.deb") || !strings.Contains(out, "Debian/Ubuntu (amd64)") {
		t.Errorf("debian preview (%v):\n%s", err, out)
	}
	out, err = runDen(t, "doctor", "--for", "vpn", "--os", "windows")
	if err != nil || !strings.Contains(out, "not available") {
		t.Errorf("windows preview (%v):\n%s", err, out)
	}
	// Ubuntu 24.04's openfortivpn predates SAML: the guide says so up front.
	out, err = runDen(t, "doctor", "--for", "vpn", "--os", "debian")
	if err != nil || !strings.Contains(out, "if apt installs one older than 1.23.0:") || !strings.Contains(out, "--enable-legacy-pppd") {
		t.Errorf("debian vpn preview (%v):\n%s", err, out)
	}
	// Without --for, the preview covers what den.yaml uses.
	out, err = runDen(t, "doctor", "--os", "fedora", "-c", tempConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"aws", "session-manager-plugin", "psql", "ssh", "sudo dnf install -y postgresql"} {
		if !strings.Contains(out, want) {
			t.Errorf("config preview lacks %q:\n%s", want, out)
		}
	}
	if _, err := runDen(t, "doctor", "--for", "sops", "--os", "plan9"); err == nil {
		t.Error("an unknown platform must be an error")
	}
}

// `den doctor --for <type>` must know every service type den can build.
func TestEveryServiceTypeIsAFeature(t *testing.T) {
	for typ := range serviceFactories {
		if typ == config.ServiceTypeKafka {
			continue // planned; nothing to connect with yet
		}
		if _, ok := prereq.Features[string(typ)]; !ok {
			t.Errorf("service type %s has no entry in prereq.Features", typ)
		}
	}
}

// den runbook new makes a folder den lists, under the name it was given, in
// the first of runbooks.dirs — read against den.yaml, not the working
// directory — or in --dir.
func TestRunbookNew(t *testing.T) {
	cfgPath := tempConfig(t)
	root := filepath.Join(filepath.Dir(cfgPath), "ops", "runbooks")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "runbooks:\n", "runbooks:\n  dirs: [ops/runbooks]\n", 1))
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runDen(t, "runbook", "new", "Rotate keys", "-c", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, filepath.Join(root, "rotate-keys")) || !strings.Contains(out, "den run 'Rotate keys'") {
		t.Fatalf("output does not say where the folder is and how to run it:\n%s", out)
	}
	out, err = runDen(t, "run", "--list", "--json", "-c", cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var got []runbookEntry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	found := false
	for _, e := range got {
		found = found || (e.Name == "Rotate keys" && e.Dir == filepath.Join(root, "rotate-keys"))
	}
	if !found {
		t.Fatalf("the new runbook is not listed with its folder: %+v", got)
	}
	if _, err := runDen(t, "runbook", "new", "Rotate keys", "-c", cfgPath); err == nil {
		t.Fatal("a second runbook of the same name was created")
	}

	elsewhere := t.TempDir()
	if out, err := runDen(t, "runbook", "new", "Other", "--dir", elsewhere, "-c", cfgPath); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "other", "runbook.yaml")); err != nil {
		t.Fatalf("--dir was not used: %v", err)
	}
}

// den runbook migrate moves a header script into a folder, but only when told
// to outside a terminal: the tests' stdin is not one.
func TestRunbookMigrate(t *testing.T) {
	cfgPath := tempConfig(t)
	root := filepath.Join(filepath.Dir(cfgPath), "runbooks")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "greet.sh"), []byte("#!/bin/sh\n# den:name Greet\necho hi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "runbooks:\n", "runbooks:\n  dirs: [runbooks]\n", 1))
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runDen(t, "runbook", "migrate", "-c", cfgPath); err == nil {
		t.Fatal("migrate without a name or --all should say what it needs")
	}
	if _, err := runDen(t, "runbook", "migrate", "Greet", "-c", cfgPath); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("migrate outside a terminal without --yes = %v, want a refusal naming --yes", err)
	}
	if _, err := os.Stat(filepath.Join(root, "greet.sh")); err != nil {
		t.Fatal("a refused migration moved the script")
	}
	out, err := runDen(t, "runbook", "migrate", "--all", "--yes", "-c", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "moved Greet into "+filepath.Join(root, "greet")) {
		t.Fatalf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "greet", "run.sh")); err != nil {
		t.Fatalf("no run.sh in the new folder: %v", err)
	}
	if out, _ := runDen(t, "runbook", "migrate", "--all", "--yes", "-c", cfgPath); !strings.Contains(out, "Nothing to migrate") {
		t.Fatalf("a second --all = %q, want nothing left", out)
	}
}

// den runbook delete says what goes and deletes it for good, but only when
// told to outside a terminal: the tests' stdin is not one.
func TestRunbookDelete(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfgPath := tempConfig(t)
	root := filepath.Join(filepath.Dir(cfgPath), "runbooks")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "runbooks:\n", "runbooks:\n  dirs: [runbooks]\n", 1))
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runDen(t, "runbook", "new", "Rotate keys", "-c", cfgPath); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	folder := filepath.Join(root, "rotate-keys")

	_, err = runDen(t, "runbook", "delete", "Rotate keys", "-c", cfgPath)
	if err == nil || !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "rotate-keys/ — 3 files") {
		t.Fatalf("delete outside a terminal without --yes = %v, want a refusal naming --yes and what goes", err)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Fatal("a refused delete deleted the folder")
	}
	out, err := runDen(t, "runbook", "rm", "Rotate keys", "--yes", "-c", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out != "deleted Rotate keys\n" {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("the folder is still there: %v", err)
	}
	if out, _ := runDen(t, "run", "--list", "--json", "-c", cfgPath); strings.Contains(out, "Rotate keys") {
		t.Errorf("still listed:\n%s", out)
	}

	if out, err := runDen(t, "runbook", "delete", "hello", "--yes", "-c", cfgPath); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if data, _ := os.ReadFile(cfgPath); strings.Contains(string(data), "hello") || !strings.Contains(string(data), "name: orders") {
		t.Errorf("den.yaml after deleting hello:\n%s", data)
	}
	if _, err := runDen(t, "runbook", "delete", "nope", "--yes", "-c", cfgPath); err == nil || !strings.Contains(err.Error(), `no runbook or sequence named "nope"`) {
		t.Errorf("an unknown name: %v", err)
	}
}

// den runbook new with a request makes the folder, starts the agent in it
// with den's MCP server and guide, says what den makes of the result, and
// exits with the agent's code.
func TestRunbookNewWithAnAgent(t *testing.T) {
	cfgPath := tempConfig(t)
	root := filepath.Join(filepath.Dir(cfgPath), "runbooks")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "runbooks:\n", "runbooks:\n  dirs: [runbooks]\n", 1))
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// A fake claude that records where it ran and with what, writes a
	// runbook as an agent would, and exits 3.
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "claude.log")
	fake := "#!/bin/sh\n" +
		"pwd > " + record + "\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + record + "; done\n" +
		"printf 'name: RDS status\\ndescription: d\\ncommand: ./run.sh\\ntimeout: 1m\\n' > runbook.yaml\n" +
		"exit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	// Only the fake: a codex installed on this machine would otherwise be
	// found, and run, where the test wants a missing one.
	t.Setenv("PATH", bin)

	out, err := runDen(t, "runbook", "new", "RDS status", "check my RDS instances, in Rust", "--agent", "claude", "-c", cfgPath)
	var exit exitCodeError
	if !errors.As(err, &exit) || exit.code != 3 {
		t.Fatalf("err = %v, want the agent's exit code 3\n%s", err, out)
	}
	folder := filepath.Join(root, "rds-status")
	logged, _ := os.ReadFile(record)
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if real, _ := filepath.EvalSymlinks(folder); lines[0] != folder && lines[0] != real {
		t.Fatalf("claude ran in %s, want %s", lines[0], folder)
	}
	args := strings.Join(lines[1:], "\n")
	for _, want := range []string{"check my RDS instances, in Rust", "--mcp-config", `"args":["mcp","-c","` + cfgPath + `"]`, "--append-system-prompt", "Writing a den runbook", "mcp__den__validate_runbook"} {
		if !strings.Contains(args, want) {
			t.Errorf("claude's args lack %q:\n%.600s", want, args)
		}
	}
	if !strings.Contains(out, "RDS status is ready (bash)") {
		t.Errorf("den should report what it makes of the folder:\n%s", out)
	}

	// A runbooks.agents entry is picked by its name and its arguments come first.
	data, _ = os.ReadFile(cfgPath)
	data = []byte(strings.Replace(string(data), "runbooks:\n", "runbooks:\n  agents:\n    - name: Claude Opus\n      command: claude --model opus\n", 1))
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runDen(t, "runbook", "new", "Opus one", "write it", "--agent", "Claude Opus", "-c", cfgPath); err == nil {
		t.Fatal("the fake claude exits 3")
	}
	logged, _ = os.ReadFile(record)
	lines = strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) < 4 || lines[1] != "--model" || lines[2] != "opus" || lines[3] != "write it" {
		t.Errorf("claude should get --model opus before the request: %q", lines)
	}

	if _, err := runDen(t, "runbook", "new", "Other", "do things", "--agent", "codex", "-c", cfgPath); err == nil || !strings.Contains(err.Error(), "codex not found") {
		t.Fatalf("a missing agent = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "other")); err == nil {
		t.Fatal("a missing agent left a folder behind")
	}
}

// A run's lines keep the colours the script printed on a terminal and lose
// them anywhere else, where they would be codes between the words.
func TestRunLinePrinterKeepsColourOnlyOnATerminal(t *testing.T) {
	const line = "\x1b[32m✔\x1b[0m ok"
	var out bytes.Buffer
	runLinePrinter(&out, true)(line)
	if out.String() != line+"\n" {
		t.Errorf("terminal = %q, want the colours kept", out.String())
	}
	out.Reset()
	runLinePrinter(&out, false)(line)
	if out.String() != "✔ ok\n" {
		t.Errorf("pipe = %q, want plain text", out.String())
	}
}

// writeDenYaml writes yaml as den.yaml in a temp folder and returns its path.
func writeDenYaml(t *testing.T, yaml string) string {
	t.Helper()
	t.Setenv("NO_COLOR", "1") // the report goes through a colour-aware writer
	path := filepath.Join(t.TempDir(), "den.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A file with a warning of each kind the check looks outside the file for:
// a misspelt key, a name used twice, and a sops file that is not there.
const warnedYaml = `tunnels:
  - name: bastion
    command: ssh -N -D 5555 bastion.example.com
  - name: bastion
    command: ssh -N -D 5556 bastion.example.com
    reconect: true
secrets:
  sops:
    - name: s
      file: /nowhere/secrets.yaml
`

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ec exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("err = %v, want an exit code", err)
	}
	return ec.code
}

func TestConfigCheckOfACleanFile(t *testing.T) {
	path := writeDenYaml(t, "tunnels:\n  - name: bastion\n    command: ssh -N -D 5555 bastion.example.com\n")
	out, err := runDen(t, "config", "--check", path)
	if err != nil || !strings.Contains(out, ": no problems") {
		t.Fatalf("err = %v, out:\n%s", err, out)
	}
}

func TestConfigCheckListsWarningsWithTheirLinesAndHints(t *testing.T) {
	path := writeDenYaml(t, warnedYaml)
	out, err := runDen(t, "config", "--check", path)
	if code := exitCode(t, err); code != 0 {
		t.Fatalf("warnings alone must not fail, exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		": 3 warnings\n",
		":6  unknown key \"reconect\", ignored\n    → did you mean \"reconnect\"?\n",
		":4  tunnels[1] \"bastion\": the name is already used by tunnels[0], and only one of them is kept\n    → rename one",
		":10  secrets.sops[0].file: /nowhere/secrets.yaml does not exist\n    → create it, or correct the path\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "! ") || strings.Contains(out, "✗") {
		t.Errorf("warnings are marked !, not ✗:\n%s", out)
	}
}

func TestConfigCheckStrictFailsOnWarnings(t *testing.T) {
	path := writeDenYaml(t, warnedYaml)
	if _, err := runDen(t, "config", "--check", "--strict", path); exitCode(t, err) != 1 {
		t.Fatalf("--strict must exit 1 on warnings, err = %v", err)
	}
	clean := writeDenYaml(t, "tunnels:\n  - name: b\n    command: ssh -N -D 5555 h\n")
	if _, err := runDen(t, "config", "--check", "--strict", clean); err != nil {
		t.Fatalf("--strict on a clean file: %v", err)
	}
}

func TestConfigCheckFailsOnErrors(t *testing.T) {
	path := writeDenYaml(t, "services:\n  - name: x\n    type: rds\n    env: eu-dev\n    rds: {rds_host: h, db_user: app}\n")
	out, err := runDen(t, "config", "--check", path)
	if exitCode(t, err) != 1 || !strings.Contains(out, "✗ ") || !strings.Contains(out, ":2  ") || !strings.Contains(out, "1 error") {
		t.Fatalf("err = %v, out:\n%s", err, out)
	}
}

func TestConfigCheckJSON(t *testing.T) {
	path := writeDenYaml(t, warnedYaml)
	out, err := runDen(t, "config", "--check", "--json", path)
	if err != nil {
		t.Fatal(err)
	}
	var got checkReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Errors != 0 || got.Warnings != 3 || len(got.Problems) != 3 || !strings.HasSuffix(got.Path, "den.yaml") {
		t.Fatalf("report = %+v", got)
	}
	first := got.Problems[0]
	if first.Severity != "warning" || first.Line != 4 || first.Code != "name-duplicate" || first.Key != "tunnels[1].name" || first.Hint == "" {
		t.Errorf("first problem = %+v", first)
	}
	// With nothing wrong the list is [], not null.
	clean := writeDenYaml(t, "tunnels: []\n")
	out, _ = runDen(t, "config", "--check", "--json", clean)
	if !strings.Contains(out, `"problems": []`) {
		t.Errorf("clean report:\n%s", out)
	}
}

func TestConfigCheckTakesTheFileFromTheFlagOrTheArgument(t *testing.T) {
	bad := writeDenYaml(t, warnedYaml)
	good := writeDenYaml(t, "tunnels: []\n")
	if out, err := runDen(t, "config", "--check", "-c", bad); err != nil || !strings.Contains(out, "3 warnings") {
		t.Errorf("-c: err %v, out:\n%s", err, out)
	}
	// The argument wins over -c.
	if out, err := runDen(t, "config", "--check", "-c", bad, good); err != nil || !strings.Contains(out, "no problems") {
		t.Errorf("argument: err %v, out:\n%s", err, out)
	}
	if _, err := runDen(t, "config", "--check", filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("a FILE that does not exist must fail")
	}
	if _, err := runDen(t, "config", good); err == nil {
		t.Error("FILE without --check must fail")
	}
	if _, err := runDen(t, "config", "--strict", "-c", good); err == nil {
		t.Error("--strict without --check must fail")
	}
}

func TestConfigShowsWarningsOnTheErrorStreamAndFailsOnErrors(t *testing.T) {
	path := writeDenYaml(t, warnedYaml)
	out, err := runDen(t, "config", "-c", path)
	if err != nil || !strings.Contains(out, "unknown key \"reconect\"") || !strings.Contains(out, "Config file: ") {
		t.Fatalf("err = %v, out:\n%s", err, out)
	}
	broken := writeDenYaml(t, "services:\n  - name: x\n    type: rds\n    env: eu-dev\n    rds: {rds_host: h, db_user: app}\n")
	if _, err := runDen(t, "config", "-c", broken); err == nil || !strings.Contains(err.Error(), "unknown env") {
		t.Fatalf("a config with errors must fail: %v", err)
	}
}

// pathsEnv points every directory den uses at temp folders, so `den paths`
// output never depends on the developer's own.
func pathsEnv(t *testing.T) (configHome, state, data string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configHome, state, data = t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_DATA_HOME", data)
	t.Chdir(t.TempDir())
	return configHome, state, data
}

func TestPathsJSON(t *testing.T) {
	cfgHome, state, data := pathsEnv(t)
	path := tempConfig(t)
	conf := filepath.Join(filepath.Dir(path), "office.conf")
	if err := os.WriteFile(conf, []byte("trusted-cert = abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg = append(cfg, "vpn:\n  - name: Office VPN\n    gateway: vpn.example.com\n    config_file: office.conf\n"...)
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runDen(t, "paths", "--json", "-c", path)
	if err != nil {
		t.Fatal(err)
	}
	var got pathsReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.ConfigSource != config.SourceFlag {
		t.Errorf("config_source = %q, want the -c flag", got.ConfigSource)
	}
	byName := map[string]pathEntry{}
	for _, e := range got.Paths {
		byName[e.Name] = e
	}
	for name, want := range map[string]pathEntry{
		"den.yaml":         {Path: path, Exists: true},
		"config directory": {Path: filepath.Join(cfgHome, "den")},
		"state directory":  {Path: filepath.Join(state, "den")},
		"data directory":   {Path: filepath.Join(data, "den")},
		"vpn Office VPN":   {Path: conf, Exists: true},
		"RDS CA bundle":    {Path: filepath.Join(cfgHome, "den", "global-bundle.pem")},
	} {
		e, ok := byName[name]
		if !ok || e.Path != want.Path || e.Exists != want.Exists {
			t.Errorf("%s = %+v, want path %s, exists %v", name, e, want.Path, want.Exists)
		}
	}
}

func TestPathsTextWithoutAConfig(t *testing.T) {
	cfgHome, _, _ := pathsEnv(t)
	out, err := runDen(t, "paths")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfgHome, "den", "den.yaml")
	for _, s := range []string{"den.yaml", "missing", want, "none found"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

func TestPathsSaysWhereTheConfigCameFrom(t *testing.T) {
	cfgHome, _, _ := pathsEnv(t)
	dir := filepath.Join(cfgHome, "den")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "den.yaml"), []byte("version: \"1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runDen(t, "paths")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "from "+config.SourceUser) || !strings.Contains(out, "exists") {
		t.Errorf("output does not say the config directory's den.yaml is in use:\n%s", out)
	}
}

// A -c file that is not there is reported, not an error: paths is how you find out why.
func TestPathsWithMissingExplicitConfig(t *testing.T) {
	pathsEnv(t)
	out, err := runDen(t, "paths", "-c", "nope.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nope.yaml") || !strings.Contains(out, "missing") {
		t.Errorf("output = %q", out)
	}
}

// vpn status names each entry's settings file and whether it is there. The
// helper part may fail on a machine without sudo; the entries come first.
func TestVPNStatusListsSettingsFiles(t *testing.T) {
	path := tempConfig(t)
	dir := filepath.Dir(path)
	if err := os.WriteFile(filepath.Join(dir, "office.conf"), []byte("trusted-cert = abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg = append(cfg, ("vpn:\n  - name: plain\n    gateway: a.example.com\n" +
		"  - name: office\n    gateway: b.example.com\n    config_file: office.conf\n" +
		"  - name: gone\n    gateway: c.example.com\n    config_file: gone.conf\n")...)
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := runDen(t, "vpn", "status", "-c", path)
	for _, want := range []string{
		"vpn entries in ",
		"plain", "gateway only, no config_file",
		"office", "office.conf (exists)",
		"gone", "gone.conf (missing)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestVPNStatusWithoutEntriesSaysTheSmallestOne(t *testing.T) {
	out, _ := runDen(t, "vpn", "status", "-c", tempConfig(t))
	for _, want := range []string{"none: add one with a gateway", "gateway: vpn.example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// The aws CLI and the SDK find a non-default file only through the environment:
// without it `aws sso login --sso-session X` looks in ~/.aws/config, which has
// no such session, and exits 255.
func TestApplyConfigExportsTheAWSFiles(t *testing.T) {
	started := startupAWSEnv
	startupAWSEnv = map[string]envValue{
		"AWS_CONFIG_FILE":             {"/users/own/config", true},
		"AWS_SHARED_CREDENTIALS_FILE": {},
	}
	t.Cleanup(func() { startupAWSEnv = started })
	// Registers the restore of both variables, whatever the test leaves behind.
	t.Setenv("AWS_CONFIG_FILE", "/users/own/config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	_ = os.Unsetenv("AWS_SHARED_CREDENTIALS_FILE")

	apply := func(cfg *config.Config) {
		t.Helper()
		mgr := service.NewManager()
		t.Cleanup(mgr.Close)
		applyConfig(t.Context(), mgr, cfg)
	}
	dir := t.TempDir()
	apply(&config.Config{AWS: &config.AWSSpec{
		ConfigPath:      filepath.Join(dir, "config-den"),
		CredentialsPath: filepath.Join(dir, "credentials-den"),
	}})
	if got, want := os.Getenv("AWS_CONFIG_FILE"), filepath.Join(dir, "config-den"); got != want {
		t.Errorf("AWS_CONFIG_FILE = %q, want %q", got, want)
	}
	if got, want := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"), filepath.Join(dir, "credentials-den"); got != want {
		t.Errorf("AWS_SHARED_CREDENTIALS_FILE = %q, want %q", got, want)
	}

	// A reload that drops the keys puts back what den started with.
	apply(&config.Config{})
	if got := os.Getenv("AWS_CONFIG_FILE"); got != "/users/own/config" {
		t.Errorf("AWS_CONFIG_FILE = %q, want the value den started with", got)
	}
	if v, ok := os.LookupEnv("AWS_SHARED_CREDENTIALS_FILE"); ok {
		t.Errorf("AWS_SHARED_CREDENTIALS_FILE = %q, want it unset again", v)
	}
}
