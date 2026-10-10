package sops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/secrets"
)

func TestFlattenKeepsDocumentOrder(t *testing.T) {
	got, err := secrets.FlattenJSON([]byte(`{"db":{"user":"app","password":"s3cr3t","port":5432},
		"hosts":["a","b"],"tls":true,"note":null,"empty":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []secrets.Field{
		field("db.user", "app"),
		field("db.password", "s3cr3t"),
		field("db.port", "5432"),
		field("hosts[0]", "a"),
		field("hosts[1]", "b"),
		field("tls", "true"),
		field("note", "null"),
	}
	if !reflect.DeepEqual(keyValues(got), want) {
		t.Errorf("flatten =\n%v\nwant\n%v", got, want)
	}
}

// fakeSOPS puts a `sops` script on PATH that runs body.
func fakeSOPS(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func encryptedFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.enc.yaml")
	if err := os.WriteFile(p, []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadPassesProfileAndDecrypts(t *testing.T) {
	// Echo the args and profile back as values, so the test sees exactly what
	// den handed to sops.
	fakeSOPS(t, `printf '{"args":"%s","profile":"%s"}' "$*" "$AWS_PROFILE"`)
	file := encryptedFile(t)
	it := New(config.SOPSSecretSpec{Name: "e2e", File: file, AWSProfile: "non-prod"}, nil)

	fields, err := it.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []secrets.Field{
		field("args", "--decrypt --output-type json "+file),
		field("profile", "non-prod"),
	}
	if !reflect.DeepEqual(keyValues(fields), want) {
		t.Errorf("fields = %v, want %v", fields, want)
	}
}

func TestReadReportsSopsErrorOnOneLine(t *testing.T) {
	fakeSOPS(t, `printf 'Failed to get the data key.\n\nGroup 0: FAILED\n  ExpiredToken\n' >&2; exit 128`)
	it := New(config.SOPSSecretSpec{Name: "e2e", File: encryptedFile(t)}, nil)

	_, err := it.Read(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); strings.Contains(got, "\n") || !strings.Contains(got, "ExpiredToken") {
		t.Errorf("err = %q, want one line keeping the cause", got)
	}
}

func TestReadDoesNotLeakPlaintextOnBadOutput(t *testing.T) {
	fakeSOPS(t, `printf 'password: hunter2'`)
	it := New(config.SOPSSecretSpec{Name: "e2e", File: encryptedFile(t)}, nil)

	_, err := it.Read(context.Background())
	if err == nil || strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "'p'") {
		t.Errorf("err = %v, must fail without quoting the output", err)
	}
}

func TestProbe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	it := New(config.SOPSSecretSpec{Name: "e2e", File: encryptedFile(t)}, nil)
	if st := it.Probe(); st.Ready || st.Missing != "sops" || !strings.HasPrefix(st.Reason, "sops not installed — ") {
		t.Errorf("no sops: %+v", st)
	}

	fakeSOPS(t, "exit 0")
	if st := it.Probe(); !st.Ready {
		t.Errorf("sops + file: %+v", st)
	}
	missing := New(config.SOPSSecretSpec{Name: "x", File: filepath.Join(t.TempDir(), "nope.yaml")}, nil)
	if st := missing.Probe(); st.Ready || st.Reason != "file not found" {
		t.Errorf("missing file: %+v", st)
	}
}

func TestEditCmd(t *testing.T) {
	it := New(config.SOPSSecretSpec{Name: "e2e", File: "/x/app.enc.yaml", AWSProfile: "non-prod"}, nil)
	cmd := it.EditCmd()
	if got := strings.Join(cmd.Args, " "); got != "sops edit /x/app.enc.yaml" {
		t.Errorf("args = %q", got)
	}
	if !contains(cmd.Env, "AWS_PROFILE=non-prod") {
		t.Error("AWS_PROFILE not passed to sops edit")
	}
}

func contains(ss []string, s string) bool {
	return slices.Contains(ss, s)
}

func TestEditResultTreatsExit200AsUnchanged(t *testing.T) {
	it := New(config.SOPSSecretSpec{Name: "e2e", File: "x"}, nil)
	run := func(code string) error { return exec.Command("sh", "-c", "exit "+code).Run() }

	if changed, err := it.EditResult(nil); !changed || err != nil {
		t.Errorf("exit 0: changed=%v err=%v", changed, err)
	}
	if changed, err := it.EditResult(run("200")); changed || err != nil {
		t.Errorf("exit 200: changed=%v err=%v, want unchanged without error", changed, err)
	}
	if _, err := it.EditResult(run("1")); err == nil {
		t.Error("exit 1: want an error")
	}
}

func field(k, v string) secrets.Field { return secrets.Field{Key: k, Value: v} }

// keyValues drops each field's Path: what is read is checked here, the path
// once, in package secrets.
func keyValues(fields []secrets.Field) []secrets.Field {
	out := make([]secrets.Field, len(fields))
	for i, f := range fields {
		out[i] = field(f.Key, f.Value)
	}
	return out
}

// awsHome writes an AWS config with an SSO profile, a static-key profile and
// a legacy SSO profile, and a cached token for the session expiring at exp.
func awsHome(t *testing.T, exp time.Time) string {
	t.Helper()
	dir := t.TempDir()
	cfg := `[sso-session corp]
sso_start_url = https://corp.awsapps.com/start
sso_region = eu-central-1

[profile sso-prof]
sso_session = corp
sso_account_id = 1
sso_role_name = dev

[profile static]
region = eu-central-1

[profile legacy]
sso_start_url = https://legacy.awsapps.com/start
`
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "sso", "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	tok := fmt.Sprintf(`{"startUrl":"https://corp.awsapps.com/start","accessToken":"x","expiresAt":%q}`,
		exp.UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(cache, "tok.json"), []byte(tok), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "config")
}

func TestProbeChecksAWSProfile(t *testing.T) {
	fakeSOPS(t, "exit 0")
	file := encryptedFile(t)
	valid := awsHome(t, time.Now().Add(time.Hour))
	expired := awsHome(t, time.Now().Add(-time.Minute))

	cases := []struct {
		name, profile, awsConfig, want string
	}{
		{"no profile", "", valid, ""},
		{"unknown profile", "acme-data-dev-typo", valid, "unknown AWS profile acme-data-dev-typo"},
		{"sso valid", "sso-prof", valid, ""},
		{"sso expired", "sso-prof", expired, "SSO login expired — log in from the AWS pane"},
		{"static keys not checked", "static", expired, ""},
		{"legacy sso without token", "legacy", valid, "SSO login expired — log in from the AWS pane"},
		{"unreadable aws config is not an error", "anything", filepath.Join(t.TempDir(), "none"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := New(config.SOPSSecretSpec{Name: "x", File: file, AWSProfile: tc.profile}, secrets.NewAWSLogins(tc.awsConfig))
			st := it.Probe()
			if st.Reason != tc.want || st.Ready != (tc.want == "") {
				t.Errorf("Probe = %+v, want reason %q", st, tc.want)
			}
		})
	}
}

// The item den built before the login must turn ready after it: nothing
// rebuilds the items when `aws sso login` finishes.
func TestProbeSeesALaterLogin(t *testing.T) {
	fakeSOPS(t, "exit 0")
	awsConfig := awsHome(t, time.Now().Add(-time.Minute))
	it := New(config.SOPSSecretSpec{Name: "x", File: encryptedFile(t), AWSProfile: "sso-prof"}, secrets.NewAWSLogins(awsConfig))
	if st := it.Probe(); st.Ready || st.Reason != "SSO login expired — log in from the AWS pane" {
		t.Fatalf("before the login: %+v", st)
	}

	tok := filepath.Join(filepath.Dir(awsConfig), "sso", "cache", "tok.json")
	body := fmt.Sprintf(`{"startUrl":"https://corp.awsapps.com/start","accessToken":"x","expiresAt":%q}`,
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if err := os.WriteFile(tok, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// The new token is as long as the old one; only the file's time tells them apart.
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(tok, later, later); err != nil {
		t.Fatal(err)
	}
	if st := it.Probe(); !st.Ready {
		t.Errorf("after the login: %+v", st)
	}
}
