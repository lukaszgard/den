package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/runbook/source"
	"github.com/lukaszgard/den/internal/service"
)

// The guardrails are the point of this package, so these tests drive the real
// protocol through a real client: what an agent can see, what needs the user's
// yes, and that no credential ever reaches it.

const secretToken = "IAM-TOKEN-host:5432/?Action=connect&X-Amz-Signature=abcdef0123456789"

type fakeSvc struct {
	name, env  string
	production bool // what den.yaml's production: or environment: prod says
	updates    chan service.StatusUpdate

	mu     sync.Mutex
	status service.Status
	token  string
	logs   []string
	stops  int
}

func newFake(name, env string) *fakeSvc {
	return &fakeSvc{name: name, env: env, production: env == "prod", updates: make(chan service.StatusUpdate, 32)}
}

func (f *fakeSvc) Info() service.ServiceInfo {
	return service.ServiceInfo{Name: f.name, Type: service.TypeRDS, Environment: f.env, Production: f.production}
}
func (f *fakeSvc) State() service.RuntimeState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return service.RuntimeState{Status: f.status, Token: f.token, LocalPort: 50111}
}
func (f *fakeSvc) Start(context.Context) (<-chan service.StatusUpdate, error) {
	f.mu.Lock()
	f.status, f.token = service.StatusConnected, secretToken
	f.logs = append(f.logs, "IAM token generated: "+secretToken, "port forwarding established on :50111")
	f.mu.Unlock()
	return f.updates, nil
}
func (f *fakeSvc) Stop(context.Context) error {
	f.mu.Lock()
	f.status = service.StatusIdle
	f.stops++
	f.mu.Unlock()
	return nil
}
func (f *fakeSvc) RefreshToken(context.Context) error { return nil }
func (f *fakeSvc) ConnectCommand() string {
	return `psql "host=localhost port=50111 dbname=app user=app sslmode=require"`
}
func (f *fakeSvc) HoldsToken() bool { return true }
func (f *fakeSvc) Logs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.logs...)
}
func (f *fakeSvc) LogSeq() uint64 { return uint64(len(f.Logs())) }

func (f *fakeSvc) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

type setup struct {
	policy    config.MCPConfig
	runbooks  []config.RunbookSpec
	dirs      []string
	envs      map[string]config.EnvSpec
	elicit    func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) // nil: client cannot elicit
	services  []*fakeSvc
	serverErr chan error
	// source, when set, is checked out as runbook source "ops" holding one
	// runbook, ops/a; trusted says the user has run it already.
	source  bool
	trusted bool
	seqs    []config.SequenceSpec
}

// start connects a client to a fresh server and returns its session; the
// server runs Run, so the session's end triggers its shutdown.
func start(t *testing.T, s *setup) *mcp.ClientSession {
	t.Helper()
	mgr := service.NewManager()
	t.Cleanup(mgr.Close)
	for _, f := range s.services {
		mgr.Register(f)
	}
	cfg := &config.Config{MCP: &s.policy, Environments: s.envs, Runbooks: &config.RunbooksConfig{Items: s.runbooks, Dirs: s.dirs, Sequences: s.seqs}}
	cfgPath := ""
	var store *source.Store
	var trust *runbook.Trust
	if s.source {
		cfgPath, store, trust = checkOutSource(t, cfg, s.trusted)
	}
	reg, err := runbook.NewRegistry(cfg, cfgPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if store != nil {
		reg.SetSources(store, trust)
	}
	srv := New(Options{Manager: mgr, Config: cfg, Runbooks: reg, Version: "test", ConnectTimeout: 5 * time.Second})

	clientT, serverT := mcp.NewInMemoryTransports()
	s.serverErr = make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { s.serverErr <- srv.Run(ctx, serverT) }()

	opts := &mcp.ClientOptions{}
	if s.elicit != nil {
		opts.ElicitationHandler = s.elicit
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, opts).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// call returns everything the agent sees of a tool call — its text and its
// structured output, as one string — and whether it was an error.
func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	text, structured, isErr := callRaw(t, cs, tool, args)
	return text + string(structured), isErr
}

func callRaw(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, []byte, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	var structured []byte
	if res.StructuredContent != nil {
		structured, _ = json.Marshal(res.StructuredContent)
	}
	return b.String(), structured, res.IsError
}

func TestListHonoursAllow(t *testing.T) {
	cs := start(t, &setup{
		policy:   config.MCPConfig{Allow: []string{"app-*"}},
		services: []*fakeSvc{newFake("app-int", "int"), newFake("billing-int", "int"), newFake("app-prod", "prod")},
	})
	text, structured, isErr := callRaw(t, cs, "list_services", nil)
	if isErr {
		t.Fatal(text)
	}
	var got servicesOutput
	if err := json.Unmarshal(structured, &got); err != nil {
		t.Fatalf("%v\n%s", err, structured)
	}
	if len(got.Services) != 2 || got.Hidden != 1 || got.Services[0].Name != "app-int" || !got.Services[1].Production {
		t.Fatalf("list = %+v", got)
	}
	if _, isErr := call(t, cs, "service_status", map[string]any{"name": "billing-int"}); !isErr {
		t.Fatal("a service outside mcp.allow must not be reachable by name")
	}
}

func TestConnectNeverReturnsTheToken(t *testing.T) {
	svc := newFake("app-int", "int")
	cs := start(t, &setup{services: []*fakeSvc{svc}})
	out, isErr := call(t, cs, "connect", map[string]any{"name": "app-int"})
	if isErr {
		t.Fatal(out)
	}
	if !strings.Contains(out, `"host":"localhost"`) || !strings.Contains(out, `"port":50111`) || !strings.Contains(out, "pgpass") {
		t.Fatalf("connect result lacks host/port/credential hint:\n%s", out)
	}
	for _, tool := range []string{"connect", "service_status", "list_services", "service_logs"} {
		out, _ := call(t, cs, tool, map[string]any{"name": "app-int"})
		if strings.Contains(out, "IAM-TOKEN") || strings.Contains(out, "abcdef0123456789") {
			t.Fatalf("%s leaked the credential:\n%s", tool, out)
		}
	}
	logs, _ := call(t, cs, "service_logs", map[string]any{"name": "app-int"})
	if !strings.Contains(logs, "[redacted]") || !strings.Contains(logs, "port forwarding established") {
		t.Fatalf("logs should be redacted, not dropped:\n%s", logs)
	}
}

func TestProductionNeedsPolicyAndConfirmation(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		svc := newFake("app-prod", "prod")
		cs := start(t, &setup{services: []*fakeSvc{svc}})
		out, isErr := call(t, cs, "connect", map[string]any{"name": "app-prod"})
		if !isErr || !strings.Contains(out, "allow_production") || svc.State().Status != service.StatusIdle {
			t.Fatalf("production connect must be refused: %s", out)
		}
	})
	t.Run("production: true gates a label that does not say prod", func(t *testing.T) {
		svc := newFake("app-live", "eu-live")
		svc.production = true
		cs := start(t, &setup{services: []*fakeSvc{svc}})
		out, isErr := call(t, cs, "connect", map[string]any{"name": "app-live"})
		if !isErr || !strings.Contains(out, "allow_production") || svc.State().Status != service.StatusIdle {
			t.Fatalf("a service marked production must be refused: %s", out)
		}
	})
	t.Run("a client that cannot ask is refused", func(t *testing.T) {
		svc := newFake("app-prod", "prod")
		cs := start(t, &setup{policy: config.MCPConfig{AllowProduction: true}, services: []*fakeSvc{svc}})
		out, isErr := call(t, cs, "connect", map[string]any{"name": "app-prod"})
		if !isErr || !strings.Contains(out, "cannot ask") || svc.State().Status != service.StatusIdle {
			t.Fatalf("without elicitation production must be refused: %s", out)
		}
	})
	for _, tc := range []struct {
		name    string
		answer  *mcp.ElicitResult
		connect bool
	}{
		{"user confirms", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, true},
		{"user declines", &mcp.ElicitResult{Action: "decline"}, false},
		{"user says no", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": false}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFake("app-prod", "prod")
			var asked string
			cs := start(t, &setup{
				policy:   config.MCPConfig{AllowProduction: true},
				services: []*fakeSvc{svc},
				elicit: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					asked = req.Params.Message
					return tc.answer, nil
				},
			})
			out, isErr := call(t, cs, "connect", map[string]any{"name": "app-prod"})
			if !strings.Contains(asked, "PRODUCTION") {
				t.Fatalf("the user was not asked about production: %q (tool said %s)", asked, out)
			}
			if connected := svc.State().Status == service.StatusConnected; connected != tc.connect || isErr == tc.connect {
				t.Fatalf("connected=%v isErr=%v, want connected=%v: %s", connected, isErr, tc.connect, out)
			}
		})
	}
}

func TestRunbooksAreOptInAndConfirmed(t *testing.T) {
	items := []config.RunbookSpec{
		{Name: "greet", Command: `echo "hello $DEN_PARAM_WHO"`, Params: []config.ParamSpec{{Name: "who", Required: true}}},
		{Name: "drop", Command: "echo dropped", Confirm: true},
	}
	cs := start(t, &setup{runbooks: items})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Listing and running runbooks are opt-in; the authoring tools, which only
	// read, are not (authoring_test.go).
	for _, tool := range tools.Tools {
		if tool.Name == "list_runbooks" || tool.Name == "run_runbook" {
			t.Fatalf("runbook tools must not exist without mcp.allow_runbooks: %s", tool.Name)
		}
	}

	cs = start(t, &setup{policy: config.MCPConfig{AllowRunbooks: true}, runbooks: items})
	if out, isErr := call(t, cs, "run_runbook", map[string]any{"name": "greet"}); !isErr || !strings.Contains(out, "who") {
		t.Fatalf("a missing required param must be reported: %s", out)
	}
	out, isErr := call(t, cs, "run_runbook", map[string]any{"name": "greet", "params": map[string]any{"who": "den"}})
	if isErr || !strings.Contains(out, "hello den") || !strings.Contains(out, `"exit_code":0`) {
		t.Fatalf("run_runbook = %s", out)
	}
	if out, isErr := call(t, cs, "run_runbook", map[string]any{"name": "drop"}); !isErr || strings.Contains(out, "dropped") {
		t.Fatalf("a confirm: true runbook must not run without the user's yes: %s", out)
	}
}

// A runbook's output is coloured for a person; an agent gets the text.
func TestRunbookOutputHasNoColourCodesForAnAgent(t *testing.T) {
	items := []config.RunbookSpec{{Name: "paint", Command: `printf '\033[32m✔\033[0m ok\n'`}}
	cs := start(t, &setup{policy: config.MCPConfig{AllowRunbooks: true}, runbooks: items})
	out, isErr := call(t, cs, "run_runbook", map[string]any{"name": "paint"})
	if isErr || !strings.Contains(out, "✔ ok") || strings.Contains(out, `\u001b`) || strings.Contains(out, "\x1b") {
		t.Fatalf("run_runbook = %s, want the text without colour codes", out)
	}
}

func TestSessionEndClosesTunnels(t *testing.T) {
	svc := newFake("app-int", "int")
	s := &setup{services: []*fakeSvc{svc}}
	cs := start(t, s)
	if out, isErr := call(t, cs, "connect", map[string]any{"name": "app-int"}); isErr {
		t.Fatal(out)
	}
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.serverErr:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not return when the client left")
	}
	if svc.stopCount() == 0 {
		t.Fatal("the tunnel an agent opened must close with its session")
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"token abc-123 used":                          "token [redacted] used",
		"url?X-Amz-Signature=deadbeef&X-Amz-Date=now": "url?X-Amz-Signature=[redacted]&X-Amz-Date=now",
		"plain line":                                  "plain line",
		strings.Repeat("A", 80):                       "[redacted]",
	} {
		if got := redact(in, "abc-123"); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfirmedRunbookRuns(t *testing.T) {
	var asked string
	cs := start(t, &setup{
		policy:   config.MCPConfig{AllowRunbooks: true},
		runbooks: []config.RunbookSpec{{Name: "drop", Command: "echo dropped", Confirm: true}},
		elicit: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked = req.Params.Message
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
		},
	})
	out, isErr := call(t, cs, "run_runbook", map[string]any{"name": "drop"})
	if isErr || !strings.Contains(out, "dropped") || !strings.Contains(asked, "echo dropped") {
		t.Fatalf("a confirmed runbook should run and the prompt should show its command: asked=%q out=%s", asked, out)
	}
}

// checkOutSource adds source "ops" to cfg and writes its checkout the way an
// update leaves it, without git: ops/a echoes "from the source".
func checkOutSource(t *testing.T, cfg *config.Config, trusted bool) (string, *source.Store, *runbook.Trust) {
	t.Helper()
	dir := t.TempDir()
	src := config.RunbookSource{Name: "ops", URL: "acme/ops"}
	cfg.Runbooks.Sources = []config.RunbookSource{src}
	store := source.NewStore(filepath.Join(dir, "data"))
	commit := strings.Repeat("a", 40)
	folder := filepath.Join(store.TreeDir(src, commit), "a")
	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "runbook.yaml"), []byte("command: echo from the source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIndex(src, commit, source.Index{Root: "r", Runbooks: map[string]string{"a": "tree-a"}}); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "den.yaml")
	if err := source.WriteLock(source.LockPath(cfgPath), source.Lock{Sources: map[string]source.Locked{"ops": {URL: src.URL, Commit: commit}}}); err != nil {
		t.Fatal(err)
	}
	trust := runbook.NewTrust(filepath.Join(dir, "trust.json"))
	if trusted {
		if err := trust.Add("ops/a", "tree-a", commit); err != nil {
			t.Fatal(err)
		}
	}
	return cfgPath, store, trust
}

func TestSourceRunbooksNeedTheirOwnOptIn(t *testing.T) {
	cs := start(t, &setup{
		policy:  config.MCPConfig{AllowRunbooks: true},
		source:  true,
		trusted: true,
		seqs:    []config.SequenceSpec{{Name: "with-source", Steps: []string{"ops/a"}}},
	})
	out, isErr := call(t, cs, "list_runbooks", nil)
	if isErr || strings.Contains(out, "ops/a") || strings.Contains(out, "with-source") {
		t.Fatalf("source runbooks, and sequences running one, are hidden without allow_source_runbooks: %s", out)
	}
	for _, name := range []string{"ops/a", "with-source"} {
		out, isErr := call(t, cs, "run_runbook", map[string]any{"name": name})
		if !isErr || !strings.Contains(out, "allow_source_runbooks") || strings.Contains(out, "from the source") {
			t.Fatalf("run_runbook %s = %s", name, out)
		}
	}
}

func TestSourceRunbooksAlwaysAsk(t *testing.T) {
	var asked string
	cs := start(t, &setup{
		policy:  config.MCPConfig{AllowRunbooks: true, AllowSourceRunbooks: true},
		source:  true,
		trusted: true, // the user ran it; an agent still asks
		elicit: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked = req.Params.Message
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
		},
	})
	out, isErr := call(t, cs, "list_runbooks", nil)
	if isErr || !strings.Contains(out, `"name":"ops/a"`) || !strings.Contains(out, `"source":"ops"`) || !strings.Contains(out, `"needs_confirmation":true`) {
		t.Fatalf("list_runbooks = %s", out)
	}
	out, isErr = call(t, cs, "run_runbook", map[string]any{"name": "ops/a"})
	if isErr || !strings.Contains(out, "from the source") {
		t.Fatalf("run_runbook = %s", out)
	}
	if !strings.Contains(asked, "runbook source ops") || !strings.Contains(asked, "echo from the source") {
		t.Errorf("the question should name the source and show the command: %q", asked)
	}
}
