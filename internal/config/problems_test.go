package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den"
)

func problemsOf(t *testing.T, yaml string) (*Config, Problems) {
	t.Helper()
	return Check([]byte(strings.TrimPrefix(yaml, "\n")))
}

func wantProblem(t *testing.T, ps Problems, sev Severity, line int, contains string) {
	t.Helper()
	for _, p := range ps {
		if p.Severity == sev && p.Line == line && strings.Contains(p.Msg, contains) {
			return
		}
	}
	t.Errorf("no problem (severity %d) at line %d containing %q in:\n%v", sev, line, contains, ps)
}

// wantHint finds the problem at line and checks its code and hint.
func wantHint(t *testing.T, ps Problems, line int, code, hint string) {
	t.Helper()
	for _, p := range ps {
		if p.Line == line && p.Code == code {
			if p.Hint != hint {
				t.Errorf("line %d (%s): hint = %q, want %q", line, code, p.Hint, hint)
			}
			return
		}
	}
	t.Errorf("no %s problem at line %d in:\n%v", code, line, ps)
}

func TestCheckReportsASyntaxErrorWithItsLine(t *testing.T) {
	cfg, ps := problemsOf(t, `
services:
  - name: a
    type: rds
   rds: {
`)
	if cfg != nil || len(ps) != 1 {
		t.Fatalf("cfg = %v, problems = %v; want nil and one problem", cfg, ps)
	}
	if ps[0].Severity != SeverityError || ps[0].Line == 0 || !strings.HasPrefix(ps[0].Msg, "not valid YAML") {
		t.Errorf("problem = %+v, want a YAML error with a line", ps[0])
	}
}

func TestCheckReportsEveryTypeErrorInPlainWords(t *testing.T) {
	cfg, ps := problemsOf(t, envBlock+`
  - name: a
    type: rds
    env: eu-dev
    rds:
      local_port: abc
      reconnect: maybe
tunnels: {name: x}
`)
	if cfg != nil {
		t.Fatal("a config with type errors must not be used")
	}
	wantProblem(t, ps, SeverityError, 15, "expected a number, got `abc`")
	wantProblem(t, ps, SeverityError, 16, "expected true or false")
	wantProblem(t, ps, SeverityError, 17, "expected a list, got a mapping")
}

func TestCheckWarnsAboutUnknownKeysAndStillLoads(t *testing.T) {
	cfg, ps := problemsOf(t, envBlock+`
  - name: a
    type: rds
    env: eu-dev
    rds:
      rds_host: db.example
      db_user: app
      reconect: true
lgos:
  level: debug
`)
	if cfg == nil {
		t.Fatalf("unknown keys are warnings, the config must load; problems: %v", ps)
	}
	if ps.Errors() != 0 || ps.Warnings() != 2 {
		t.Fatalf("problems = %v, want 2 warnings", ps)
	}
	wantProblem(t, ps, SeverityWarning, 17, `unknown key "reconect", ignored`)
	wantProblem(t, ps, SeverityWarning, 18, `unknown key "lgos", ignored`)
	wantHint(t, ps, 17, "unknown-key", `did you mean "reconnect"?`)
	wantHint(t, ps, 18, "unknown-key", `did you mean "logs"?`)
}

func TestCheckDoesNotSuggestAKeyThatIsTooFarOff(t *testing.T) {
	_, ps := problemsOf(t, "services: []\nfrobnicate: true\n")
	if len(ps) != 1 || ps[0].Hint != "" {
		t.Errorf("problems = %v, want one warning without a suggestion", ps)
	}
}

func TestCheckReportsEveryValidationErrorWithItsEntrysLine(t *testing.T) {
	cfg, ps := problemsOf(t, `
services:
  - name: a
    type: rds
  - name: b
    type: valkey
tunnels:
  - name: t
vpn:
  - name: v
secrets:
  sops:
    - name: s
`)
	if cfg != nil {
		t.Fatal("an invalid config must not be used")
	}
	if ps.Errors() != 5 {
		t.Fatalf("got %d errors, want one per broken entry (5):\n%v", ps.Errors(), ps)
	}
	wantProblem(t, ps, SeverityError, 2, `service[0] "a": type=rds but no rds section`)
	wantProblem(t, ps, SeverityError, 4, `service[1] "b": unknown type "valkey"`)
	wantProblem(t, ps, SeverityError, 7, `tunnel[0] "t": command is required`)
	wantProblem(t, ps, SeverityError, 9, `vpn[0] "v": gateway or config_file is required`)
	wantProblem(t, ps, SeverityError, 12, `secrets.sops[0] "s": file is required`)
	if err := ps.Err("/x/den.yaml"); err == nil || !strings.Contains(err.Error(), "\n  /x/den.yaml:7: tunnel[0]") {
		t.Errorf("Err = %v, want a path:line: message line per error", err)
	}
}

func TestCheckPointsAnUnknownEnvAtItsService(t *testing.T) {
	_, ps := problemsOf(t, `
services:
  - name: a
    type: rds
  - name: b
    type: rds
    env: nowhere
    rds: {rds_host: x, db_user: app}
`)
	wantProblem(t, ps, SeverityError, 4, `service[1] "b": unknown env "nowhere"`)
	for _, p := range ps {
		if p.Key != "services[1]" || p.Code != "invalid" {
			t.Errorf("problem %+v, want key services[1] and code invalid", p)
		}
	}
}

func TestCheckWarnsAboutUnknownPanelsAndLogLevels(t *testing.T) {
	cfg, ps := problemsOf(t, `
menu_order:
  - connect
  - dupa
  - runbok
disabled_panes: [vpn, nope]
logs:
  level: verbose
`)
	if cfg == nil {
		t.Fatalf("these are warnings, the config must load: %v", ps)
	}
	wantProblem(t, ps, SeverityWarning, 3, `menu_order: unknown panel "dupa", ignored`)
	wantProblem(t, ps, SeverityWarning, 4, `menu_order: unknown panel "runbok", ignored`)
	for _, p := range ps {
		if p.Line == 3 && !strings.HasPrefix(p.Hint, "panels: connect, tunnel") {
			t.Errorf("hint for a far-off panel = %q, want the panel list", p.Hint)
		}
	}
	wantHint(t, ps, 4, "unknown-panel", `did you mean "runbooks"?`)
	wantProblem(t, ps, SeverityWarning, 5, `disabled_panes: unknown panel "nope"`)
	wantProblem(t, ps, SeverityWarning, 7, `logs.level: "verbose" is not a level`)
	for _, p := range ps {
		if p.Code == "log-level" && p.Key != "logs.level" {
			t.Errorf("log-level key = %q", p.Key)
		}
	}
}

func TestProblemLinesAddTheHintAfterAnArrow(t *testing.T) {
	p := Problem{Msg: "unknown key", Hint: "did you mean x?"}
	if got := p.Lines(); !slices.Equal(got, []string{"unknown key", "→ did you mean x?"}) {
		t.Errorf("Lines = %q", got)
	}
	if got := (Problem{Msg: "m"}).Lines(); !slices.Equal(got, []string{"m"}) {
		t.Errorf("Lines without a hint = %q", got)
	}
}

func TestCheckAcceptsTheLevelsTheLoggerAccepts(t *testing.T) {
	for _, level := range []string{"trace", "DEBUG", "info", "warning", "Warn", "error"} {
		if _, ps := problemsOf(t, "logs: {level: "+level+"}\n"); len(ps) != 0 {
			t.Errorf("level %q: %v", level, ps)
		}
	}
}

func TestCheckOfAnEmptyFileIsAnEmptyConfig(t *testing.T) {
	cfg, ps := problemsOf(t, "")
	if cfg == nil || len(ps) != 0 {
		t.Errorf("cfg = %v, problems = %v; want an empty config and no problems", cfg, ps)
	}
}

func TestCheckOrdersErrorsBeforeWarnings(t *testing.T) {
	_, ps := problemsOf(t, "foo: 1\ntunnels:\n  - name: t\n")
	if len(ps) != 2 || ps[0].Severity != SeverityError || ps[1].Severity != SeverityWarning {
		t.Errorf("problems = %v, want the error first", ps)
	}
	if got := ps.Summary(); got != "1 error, 1 warning" {
		t.Errorf("Summary = %q", got)
	}
}

// The menu's panels are in the schema too, so an editor underlines a panel
// den does not have.
func TestSchemaListsThePanelNames(t *testing.T) {
	var s struct {
		Properties map[string]struct {
			Items struct {
				Enum []string `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(den.Schema, &s); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"menu_order", "disabled_panes"} {
		if got := s.Properties[key].Items.Enum; !slices.Equal(got, PaneNames) {
			t.Errorf("schema %s items enum = %v, want %v (run make generate)", key, got, PaneNames)
		}
	}
}

func TestEditDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"", "abc", 3}, {"reconect", "reconnect", 1}, {"regoin", "region", 2}, {"same", "same", 0}} {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckRequiresAServiceName(t *testing.T) {
	cfg, ps := problemsOf(t, `
services:
  - type: rds
    rds: {rds_host: x, db_user: app}
`)
	if cfg != nil {
		t.Fatal("a service without a name must not load")
	}
	wantProblem(t, ps, SeverityError, 2, "service[0]: name is required")
}
