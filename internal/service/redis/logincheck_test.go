package redis

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

// The check is a tab's redis-cli plus PING. redis-cli exits 0 when the cache
// answers WRONGPASS, so success is the PONG.
func TestLoginProbe(t *testing.T) {
	e := &engine{spec: &config.RedisSpec{LocalPort: 16380, UserID: "app", CacheName: "orders"}}

	p := e.LoginProbe("tok-1")
	tab := e.TerminalCommand("tok-1")
	want := service.ClientCommand{Tool: "redis-cli", Args: append(slices.Clone(tab.Args), "PING"), Env: tab.Env}
	if !reflect.DeepEqual(p.Command, want) {
		t.Fatalf("probe =\n%+v\nwant\n%+v", p.Command, want)
	}
	if p.Succeeded == nil {
		t.Fatal("Succeeded is nil: exit status 0 would count WRONGPASS as a login")
	}
	if !p.Succeeded("PONG\n") || p.Succeeded("AUTH failed: WRONGPASS invalid username-password pair\nNOAUTH Authentication required.\n") {
		t.Error("Succeeded must take PONG and nothing else")
	}
	if p.User != "app" {
		t.Errorf("User = %q, want app", p.User)
	}
}

func TestLoginProbeWithoutIAMSendsNoPassword(t *testing.T) {
	e := &engine{spec: &config.RedisSpec{LocalPort: 16380}}
	if p := e.LoginProbe(""); p.Command.Env != nil || p.User != "" {
		t.Fatalf("Env = %q, User = %q; want none", p.Command.Env, p.User)
	}
}

func TestLoginHint(t *testing.T) {
	e := &engine{
		spec: &config.RedisSpec{LocalPort: 16380, UserID: "app", CacheName: "orders"},
		env:  config.EnvMapping{CredentialProfile: "acme-int"},
	}
	hint := e.LoginProbe("tok").Hint
	for _, want := range []string{`AWS profile "acme-int"`, `user "app"`, `"orders"`, "elasticache:Connect", "user group", "aws sso login --profile acme-int"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint has no %q:\n%s", want, hint)
		}
	}
	e.spec.MemoryDB = true
	if hint := e.LoginProbe("tok").Hint; !strings.Contains(hint, "memorydb:Connect") || !strings.Contains(hint, "ACL") {
		t.Errorf("MemoryDB hint:\n%s", hint)
	}
	plain := &engine{spec: &config.RedisSpec{LocalPort: 16380}}
	if hint := plain.LoginProbe("").Hint; !strings.Contains(hint, "cache_name and user_id") {
		t.Errorf("hint without IAM:\n%s", hint)
	}
}

// The hint is read in the detail view's log, which shows 104 runes of a line
// at 120 columns and cuts the rest.
func TestLoginHintLinesFitTheLog(t *testing.T) {
	e := &engine{
		spec: &config.RedisSpec{LocalPort: 16380, UserID: "cn-e2e-orders-service-iam", CacheName: "cn-e2e-orders-sessions-cache", MemoryDB: true},
		env:  config.EnvMapping{CredentialProfile: "acme-cn-e2e-orders-admin"},
	}
	for line := range strings.Lines(e.LoginProbe("tok").Hint) {
		if n := len([]rune(strings.TrimRight(line, "\n"))); n > 104 {
			t.Errorf("a line of %d runes:\n%s", n, line)
		}
	}
}
