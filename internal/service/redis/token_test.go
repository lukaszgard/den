package redis

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/service"
)

var testCreds = aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "session"}

func parseToken(t *testing.T, token string) (host string, q url.Values) {
	t.Helper()
	if strings.HasPrefix(token, "http") {
		t.Fatalf("token %q must not carry a scheme", token)
	}
	u, err := url.Parse("http://" + token)
	if err != nil {
		t.Fatalf("token is not a URL: %v", err)
	}
	return u.Host, u.Query()
}

func TestTokenTargetSigningName(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		spec         config.RedisSpec
		wantScope    string
		wantResource string
	}{
		{
			name:      "elasticache",
			spec:      config.RedisSpec{CacheName: "app-int", UserID: "app-int"},
			wantScope: "20260922/eu-central-1/elasticache/aws4_request",
		},
		{
			name:         "elasticache serverless",
			spec:         config.RedisSpec{CacheName: "orders-sls", UserID: "app-int", Serverless: true},
			wantScope:    "20260922/eu-central-1/elasticache/aws4_request",
			wantResource: "ServerlessCache",
		},
		{
			name:      "memorydb",
			spec:      config.RedisSpec{CacheName: "cluster-01", UserID: "iam-user-01", MemoryDB: true},
			wantScope: "20260922/eu-central-1/memorydb/aws4_request",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &engine{spec: &c.spec, env: config.EnvMapping{AWSRegionCode: "eu-central-1"}}
			token, err := presignToken(context.Background(), testCreds, e.tokenTarget(), now)
			if err != nil {
				t.Fatal(err)
			}
			host, q := parseToken(t, token)
			if host != c.spec.CacheName {
				t.Errorf("host = %q, want the cache name %q", host, c.spec.CacheName)
			}
			if q.Get("Action") != "connect" || q.Get("User") != c.spec.UserID {
				t.Errorf("Action/User = %q/%q", q.Get("Action"), q.Get("User"))
			}
			if q.Get("X-Amz-Expires") != "900" {
				t.Errorf("X-Amz-Expires = %q, want 900", q.Get("X-Amz-Expires"))
			}
			if cred := q.Get("X-Amz-Credential"); cred != "AKIDEXAMPLE/"+c.wantScope {
				t.Errorf("X-Amz-Credential = %q, want scope %q", cred, c.wantScope)
			}
			if got := q.Get("ResourceType"); got != c.wantResource {
				t.Errorf("ResourceType = %q, want %q", got, c.wantResource)
			}
			if q.Get("X-Amz-Security-Token") != "session" || q.Get("X-Amz-Signature") == "" {
				t.Error("token must carry the session token and a signature")
			}
		})
	}
}

// y copies the connect command and must never carry the token; Y inlines it.
func TestConnectCommandKeepsTheTokenOutUnlessAsked(t *testing.T) {
	e := &engine{spec: &config.RedisSpec{LocalPort: 16380, UserID: "app"}}
	if got, want := e.ConnectCommand(), "redis-cli -h localhost -p 16380 --tls --user app"; got != want {
		t.Fatalf("ConnectCommand = %q, want %q", got, want)
	}
	if got, want := e.ConnectCommandWithSecret("tok"), `redis-cli -h localhost -p 16380 --tls --user app -a "tok"`; got != want {
		t.Fatalf("ConnectCommandWithSecret = %q, want %q", got, want)
	}
}

// The terminal command is run by den, so the token must not sit where other
// users of the machine can read it: redis-cli takes it from REDISCLI_AUTH
// instead of -a.
func TestTerminalCommandPassesTheTokenInTheEnvironment(t *testing.T) {
	e := &engine{spec: &config.RedisSpec{LocalPort: 16380, UserID: "app"}}

	got := e.TerminalCommand("tok-1")
	want := service.ClientCommand{
		Tool: "redis-cli",
		Args: []string{"-h", "localhost", "-p", "16380", "--tls", "--user", "app"},
		Env:  []string{"REDISCLI_AUTH=tok-1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
	// The command den shows and the one it runs must reach the same cache.
	if shown := e.ConnectCommand(); shown != "redis-cli "+strings.Join(got.Args, " ") {
		t.Fatalf("the shown command %s does not match the run one %q", shown, got.Args)
	}
}

// A cache reached without IAM has no token. An empty REDISCLI_AUTH would make
// redis-cli send an empty password, which the cache answers with an error.
func TestTerminalCommandWithoutATokenSetsNoPassword(t *testing.T) {
	e := &engine{spec: &config.RedisSpec{LocalPort: 16380}}

	got := e.TerminalCommand("")
	want := service.ClientCommand{Tool: "redis-cli", Args: []string{"-h", "localhost", "-p", "16380", "--tls"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
}
