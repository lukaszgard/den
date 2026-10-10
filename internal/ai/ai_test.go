package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

func TestRedactAndRestore(t *testing.T) {
	r := NewRedactor(map[string][]string{
		"profile": {"acme-devops-prod", "acme"},
		"service": {"EU PROD"},
	})
	in := strings.Join([]string{
		"AWS profiles: not in /Users/jane.doe/.aws/config: acme-devops-prod (EU PROD)",
		"bastion i-0123456789abcdef0: could not ask SSM as acme",
		"role arn:aws:iam::111122223333:role/UserFull, account 111122223333",
		"host app-prod.abcdefghijkl.eu-central-1.rds.amazonaws.com at 10.0.3.7, ask ops@corp.example",
		"aws-cli/2.27.28, see https://docs.aws.amazon.com/cli/latest/userguide/x.html, file den.yaml",
	}, "\n")
	out := r.Redact(in)
	for _, secret := range []string{"acme", "EU PROD", "jane.doe", "i-0123456789abcdef0", "111122223333",
		"abcdefghijkl", "10.0.3.7", "ops@corp.example", "arn:aws"} {
		if strings.Contains(out, secret) {
			t.Errorf("redacted text still contains %q:\n%s", secret, out)
		}
	}
	for _, kept := range []string{"aws-cli/2.27.28", "docs.aws.amazon.com", "den.yaml", "~/.aws/config"} {
		if !strings.Contains(out, kept) {
			t.Errorf("redaction removed %q, which identifies nothing:\n%s", kept, out)
		}
	}
	if !strings.Contains(out, "profile-1 (service-1)") {
		t.Errorf("explicit terms should become stable placeholders:\n%s", out)
	}

	answer := "Add profile-1 to your AWS config; service-1 uses it. instance-1 must be online."
	restored := r.Restore(answer)
	if restored != "Add acme-devops-prod to your AWS config; EU PROD uses it. i-0123456789abcdef0 must be online." {
		t.Errorf("Restore = %q", restored)
	}
}

func TestPlaceholdersDoNotCollide(t *testing.T) {
	var hosts []string
	for i := range 12 {
		hosts = append(hosts, "db"+strings.Repeat("x", i)+".corp.example.com")
	}
	r := NewRedactor(nil)
	out := r.Redact(strings.Join(hosts, " "))
	if !strings.Contains(out, "host-12") {
		t.Fatalf("expected 12 host placeholders: %s", out)
	}
	if got := r.Restore("host-12 and host-1"); got != hosts[11]+" and "+hosts[0] {
		t.Fatalf("host-12 must not be restored as host-1 + 2: %q", got)
	}
}

func TestExplainSendsFallbacksAndReadsText(t *testing.T) {
	var body map[string]any
	var beta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		beta = r.Header.Get("anthropic-beta")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5",
			"content":[{"type":"text","text":"Log in with aws sso login."}],
			"stop_reason":"end_turn","stop_details":null,"usage":{"input_tokens":10,"output_tokens":5}}`)
	}))
	defer srv.Close()

	c := NewClaude("", option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	got, err := c.Explain(context.Background(), "⚠ SSO profile-1  login expired")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Log in with aws sso login." {
		t.Errorf("answer = %q", got)
	}
	if body["model"] != DefaultModel || body["fallbacks"] != "default" || !strings.Contains(beta, "server-side-fallback-2026-07-01") {
		t.Errorf("request: model=%v fallbacks=%v beta=%q", body["model"], body["fallbacks"], beta)
	}
	msgs, _ := body["messages"].([]any)
	if raw, _ := json.Marshal(msgs); !strings.Contains(string(raw), "login expired") {
		t.Errorf("the report was not sent: %s", raw)
	}
}

func TestExplainReportsRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],
			"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"x"},
			"usage":{"input_tokens":10,"output_tokens":0}}`)
	}))
	defer srv.Close()
	_, err := NewClaude("", option.WithBaseURL(srv.URL), option.WithAPIKey("test")).Explain(context.Background(), "r")
	if err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("err = %v, want a refusal error", err)
	}
}

func TestExplainTurnsAuthErrorIntoAHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()
	_, err := NewClaude("", option.WithBaseURL(srv.URL), option.WithAPIKey("bad"), option.WithMaxRetries(0)).Explain(context.Background(), "r")
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("err = %v, want a credentials hint", err)
	}
}
