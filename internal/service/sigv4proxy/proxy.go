// Package sigv4proxy serves a local plain-HTTP endpoint that forwards every
// request to an AWS service over an SSM tunnel, signed with SigV4.
//
// Services such as OpenSearch and Neptune have no password or token to hand a
// client: every request must carry its own signature, computed over the real
// endpoint's Host header. Through a tunnel the client talks to localhost, so
// neither signing tools nor TLS verification line up. The proxy puts them back:
// it rewrites Host to the real endpoint, signs for it, and dials the tunnel with
// TLS ServerName set to the real endpoint, so certificate verification stays on.
//
// Anything that can reach the listener acts with the caller's IAM identity, so
// it binds to 127.0.0.1 only.
package sigv4proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// Config describes one proxy.
type Config struct {
	ListenPort   int    // user-facing port on 127.0.0.1
	UpstreamHost string // real endpoint hostname: Host header, TLS ServerName, signature
	UpstreamPort int    // real endpoint port (part of Host unless 443)
	DialAddr     string // where connections actually go: the local end of the SSM tunnel
	Service      string // SigV4 signing name, e.g. "es", "aoss", "neptune-db"
	Region       string
	Credentials  aws.CredentialsProvider
	RootCAs      *x509.CertPool // nil = system roots
	Log          func(string)
}

// Proxy is a running signing proxy.
type Proxy struct {
	srv *http.Server
	ln  net.Listener
}

// Start listens on 127.0.0.1:cfg.ListenPort and serves in the background.
func Start(cfg Config) (*Proxy, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.ListenPort)))
	if err != nil {
		return nil, fmt.Errorf("signing proxy: %w", err)
	}
	log := cfg.Log
	if log == nil {
		log = func(string) {}
	}

	hostport := cfg.UpstreamHost
	if cfg.UpstreamPort != 0 && cfg.UpstreamPort != 443 {
		hostport = net.JoinHostPort(cfg.UpstreamHost, strconv.Itoa(cfg.UpstreamPort))
	}
	upstream := &url.URL{Scheme: "https", Host: hostport}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", cfg.DialAddr)
		},
		TLSClientConfig:     &tls.Config{ServerName: cfg.UpstreamHost, RootCAs: cfg.RootCAs, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Remember the host the client used (localhost vs 127.0.0.1 matters
			// to a browser's cookies) for rewriting redirects on the way back.
			pr.Out = pr.Out.WithContext(context.WithValue(pr.Out.Context(), clientHostKey{}, pr.In.Host))
			pr.SetURL(upstream)
			// Anything auth-shaped the client sent would either clash with or
			// ride along beside our signature.
			for _, h := range []string{"Authorization", "X-Amz-Date", "X-Amz-Security-Token", "X-Amz-Content-Sha256"} {
				pr.Out.Header.Del(h)
			}
		},
		Transport: &signer{
			base:    transport,
			creds:   cfg.Credentials,
			service: cfg.Service,
			region:  cfg.Region,
			v4:      v4.NewSigner(),
		},
		ModifyResponse: func(resp *http.Response) error {
			clientHost, _ := resp.Request.Context().Value(clientHostKey{}).(string)
			rewriteLocation(resp, upstream, clientHost)
			if resp.StatusCode >= 400 {
				log(fmt.Sprintf("%s %s → %s", resp.Request.Method, resp.Request.URL.RequestURI(), resp.Status))
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log(fmt.Sprintf("%s %s → proxy error: %v", r.Method, r.URL.RequestURI(), err))
			http.Error(w, "den signing proxy: "+err.Error(), http.StatusBadGateway)
		},
	}

	p := &Proxy{
		srv: &http.Server{Handler: rp, ReadHeaderTimeout: 30 * time.Second},
		ln:  ln,
	}
	go func() {
		if err := p.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log(fmt.Sprintf("signing proxy stopped: %v", err))
		}
	}()
	return p, nil
}

// Addr is the address the proxy listens on.
func (p *Proxy) Addr() net.Addr { return p.ln.Addr() }

// Close stops the proxy and drops open connections.
func (p *Proxy) Close() error { return p.srv.Close() }

// signer signs each outgoing request just before it is sent, so nothing can
// change a signed header afterwards.
type signer struct {
	base    http.RoundTripper
	creds   aws.CredentialsProvider
	service string
	region  string
	v4      *v4.Signer
}

func (s *signer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())

	// SigV4 covers the payload hash, so the body has to be read up front.
	var body []byte
	if req.Body != nil && req.Body != http.NoBody {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		req.Body.Close()
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	req.ContentLength = int64(len(body))
	if len(body) == 0 {
		req.Body = http.NoBody
	}

	sum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(sum[:])
	// OpenSearch Serverless rejects requests without it; the others accept it.
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	creds, err := s.creds.Retrieve(req.Context())
	if err != nil {
		return nil, fmt.Errorf("retrieve credentials: %w", err)
	}
	if err := s.v4.SignHTTP(req.Context(), creds, req, payloadHash, s.service, s.region, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}
	return s.base.RoundTrip(req)
}

type clientHostKey struct{}

// rewriteLocation points absolute redirects at the upstream back at the proxy,
// as reached by the client, so a browser following them stays on the tunnel.
func rewriteLocation(resp *http.Response, upstream *url.URL, clientHost string) {
	loc := resp.Header.Get("Location")
	if loc == "" || clientHost == "" {
		return
	}
	u, err := url.Parse(loc)
	if err != nil || !u.IsAbs() || u.Host != upstream.Host {
		return
	}
	u.Scheme, u.Host = "http", clientHost
	resp.Header.Set("Location", u.String())
}
