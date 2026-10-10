package sigv4proxy

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

var testCreds = aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "session"}

type seen struct {
	host, authz, token, contentSHA, body, path string
	sigValid                                   bool
	sigErr                                     string
}

// upstream is a TLS server standing in for the AWS endpoint. httptest's
// certificate is issued for example.com, so a proxy that sets ServerName to
// the real endpoint verifies it without skipping verification.
func upstream(t *testing.T, service string, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, chan seen) {
	t.Helper()
	got := make(chan seen, 4)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ok, why := verifySignature(r, body, service)
		got <- seen{
			host: r.Host, authz: r.Header.Get("Authorization"), token: r.Header.Get("X-Amz-Security-Token"),
			contentSHA: r.Header.Get("X-Amz-Content-Sha256"), body: string(body), path: r.URL.RequestURI(),
			sigValid: ok, sigErr: why,
		}
		if handler != nil {
			handler(w, r)
			return
		}
		fmt.Fprint(w, `{"status":"green"}`)
	}))
	t.Cleanup(ts.Close)
	return ts, got
}

// verifySignature re-signs the request as received, using only the headers its
// signature names, the way AWS validates it. A header altered after signing —
// by the proxy or its transport — shows up as a mismatch.
func verifySignature(r *http.Request, body []byte, service string) (bool, string) {
	authz := r.Header.Get("Authorization")
	_, signedPart, ok := strings.Cut(authz, "SignedHeaders=")
	if !ok {
		return false, "no SignedHeaders"
	}
	signedHeaders, _, _ := strings.Cut(signedPart, ",")
	signingTime, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return false, "bad X-Amz-Date"
	}

	req, _ := http.NewRequest(r.Method, "https://"+r.Host+r.URL.RequestURI(), nil)
	req.ContentLength = r.ContentLength
	for h := range strings.SplitSeq(signedHeaders, ";") {
		switch h {
		case "host", "content-length", "x-amz-date", "x-amz-security-token":
			// the signer derives these itself
		default:
			req.Header[http.CanonicalHeaderKey(h)] = r.Header.Values(h)
		}
	}
	sum := sha256.Sum256(body)
	if err := v4.NewSigner().SignHTTP(context.Background(), testCreds, req, hex.EncodeToString(sum[:]), service, "eu-central-1", signingTime); err != nil {
		return false, err.Error()
	}
	if want := req.Header.Get("Authorization"); want != authz {
		return false, fmt.Sprintf("signature mismatch:\n got  %s\n want %s", authz, want)
	}
	return true, ""
}

func testPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startProxy(t *testing.T, ts *httptest.Server, service string, upstreamPort int) (*Proxy, *logSink) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	logs := &logSink{}
	p, err := Start(Config{
		ListenPort:   testPort(t),
		UpstreamHost: "example.com",
		UpstreamPort: upstreamPort,
		DialAddr:     ts.Listener.Addr().String(),
		Service:      service,
		Region:       "eu-central-1",
		Credentials:  aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return testCreds, nil }),
		RootCAs:      pool,
		Log:          logs.add,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, logs
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) add(s string) { l.mu.Lock(); l.lines = append(l.lines, s); l.mu.Unlock() }
func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func TestSignsForRealEndpoint(t *testing.T) {
	ts, got := upstream(t, "es", nil)
	p, _ := startProxy(t, ts, "es", 443)

	body := `{"query":{"match_all":{}}}`
	resp, err := http.Post("http://"+p.Addr().String()+"/logs-*/_search?size=1", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	s := <-got
	if s.host != "example.com" {
		t.Errorf("Host = %q, want the real endpoint (443 is implied)", s.host)
	}
	if !strings.Contains(s.authz, "Credential=AKIDEXAMPLE/") || !strings.Contains(s.authz, "/eu-central-1/es/aws4_request") {
		t.Errorf("Authorization = %q, want an es/eu-central-1 scope", s.authz)
	}
	if s.token != "session" {
		t.Errorf("X-Amz-Security-Token = %q", s.token)
	}
	sum := sha256.Sum256([]byte(body))
	if s.contentSHA != hex.EncodeToString(sum[:]) || s.body != body {
		t.Errorf("body/hash not forwarded intact: body %q sha %q", s.body, s.contentSHA)
	}
	if s.path != "/logs-*/_search?size=1" {
		t.Errorf("path = %q", s.path)
	}
	if !s.sigValid {
		t.Errorf("signature does not verify: %s", s.sigErr)
	}
}

func TestNonDefaultPortIsPartOfHost(t *testing.T) {
	ts, got := upstream(t, "neptune-db", nil)
	p, _ := startProxy(t, ts, "neptune-db", 8182)

	resp, err := http.Get("http://" + p.Addr().String() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	s := <-got
	if s.host != "example.com:8182" {
		t.Errorf("Host = %q, want example.com:8182 — Neptune signs over host:port", s.host)
	}
	if !strings.Contains(s.authz, "/neptune-db/aws4_request") {
		t.Errorf("Authorization = %q, want a neptune-db scope", s.authz)
	}
	if !s.sigValid {
		t.Errorf("signature does not verify: %s", s.sigErr)
	}
}

func TestDropsClientAuthAndRewritesRedirects(t *testing.T) {
	ts, got := upstream(t, "es", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/_dashboards/app/home", http.StatusFound)
	})
	p, _ := startProxy(t, ts, "es", 443)

	port := p.Addr().(*net.TCPAddr).Port
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:%d/_dashboards", port), nil)
	req.Header.Set("Authorization", "Basic c29tZW9uZTplbHNl")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if s := <-got; !strings.HasPrefix(s.authz, "AWS4-HMAC-SHA256 ") || !s.sigValid {
		t.Errorf("client Authorization leaked or signature invalid: %q (%s)", s.authz, s.sigErr)
	}
	// localhost, as the client asked — not 127.0.0.1, which would lose the
	// browser's cookies.
	if loc, want := resp.Header.Get("Location"), fmt.Sprintf("http://localhost:%d/_dashboards/app/home", port); loc != want {
		t.Errorf("Location = %q, want %q so the browser stays on the proxy", loc, want)
	}
}

func TestUntrustedCertificateFails(t *testing.T) {
	ts, _ := upstream(t, "es", nil)
	logs := &logSink{}
	p, err := Start(Config{
		ListenPort:   testPort(t),
		UpstreamHost: "example.com",
		DialAddr:     ts.Listener.Addr().String(),
		Service:      "es",
		Region:       "eu-central-1",
		Credentials:  aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return testCreds, nil }),
		// no RootCAs: the test server's self-signed certificate is not trusted
		Log: logs.add,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	resp, err := http.Get("http://" + p.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: certificate verification must stay on", resp.StatusCode)
	}
	if !strings.Contains(logs.String(), "proxy error") {
		t.Errorf("proxy error not logged; logs:\n%s", logs)
	}
}

func TestListensOnLoopbackOnly(t *testing.T) {
	ts, _ := upstream(t, "es", nil)
	p, _ := startProxy(t, ts, "es", 443)
	if ip := p.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
		t.Fatalf("listening on %v; the proxy signs as the user and must not be reachable off-host", ip)
	}
}

// Neptune's Gremlin console talks WebSocket; the signed upgrade request must
// reach the endpoint intact and the upgraded stream must flow both ways.
func TestWebSocketUpgradePassesThrough(t *testing.T) {
	ts, got := upstream(t, "neptune-db", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		line, _ := rw.ReadString('\n')
		_, _ = rw.WriteString("echo: " + line)
		rw.Flush()
	})
	p, _ := startProxy(t, ts, "neptune-db", 8182)

	conn, err := net.Dial("tcp", p.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET /gremlin HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if s := <-got; !s.sigValid {
		t.Fatalf("upgrade request signature does not verify: %s", s.sigErr)
	}

	fmt.Fprint(conn, "g.V().count()\n")
	reply, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if reply != "echo: g.V().count()\n" {
		t.Fatalf("reply = %q", reply)
	}
}
