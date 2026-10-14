// Package mcpserver exposes den to AI agents over the Model Context Protocol:
// an agent can list the services den.yaml defines, open a tunnel, read its
// status and logs, write runbooks with den's guide, and — when allowed — run
// them.
//
// The boundary is deliberate. Credentials never leave den: a tool reports how
// a client authenticates (pgpass, an option file, the AWS profile), never the
// token. What an agent may touch is set by the mcp: block of den.yaml, and a
// production connect or a runbook marked confirm: true needs the user's
// explicit yes in their MCP client — without a client that can ask, it is
// refused.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/pgpass"
)

// Options is what the server serves.
type Options struct {
	Manager  *service.Manager
	Config   *config.Config // nil without a config file
	Runbooks *runbook.Registry
	Version  string
	// ConnectTimeout bounds how long connect waits for a tunnel to come up;
	// 90 s when zero.
	ConnectTimeout time.Duration
}

// Server is den's MCP server.
type Server struct {
	opts   Options
	policy config.MCPConfig
	mcp    *mcp.Server
	// ctx outlives single tool calls: the tunnels a session opens run under
	// it, and end with it.
	ctx context.Context
}

const instructions = `den opens tunnels to private cloud databases and caches (RDS, Redis,
DocumentDB, Redshift, OpenSearch, Neptune) and keeps their IAM credentials fresh.
Call list_services to see what is available, connect to open a tunnel, then use the
returned connect_command (host is always localhost) with your own database tool.
Credentials are never returned: the connect result says how the client authenticates.
Production services and some runbooks need the user's confirmation. disconnect when done.
To write a runbook, read runbook_guide first and finish with validate_runbook.`

// New builds the server and registers its tools.
func New(opts Options) *Server {
	if opts.ConnectTimeout == 0 {
		opts.ConnectTimeout = 90 * time.Second
	}
	s := &Server{opts: opts, ctx: context.Background()}
	if opts.Config != nil && opts.Config.MCP != nil {
		s.policy = *opts.Config.MCP
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "den", Version: opts.Version}, &mcp.ServerOptions{Instructions: instructions})
	s.addTools()
	return s
}

// Run serves one client over t until it disconnects or ctx ends, then stops
// every tunnel and runbook the session started.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	// Nothing else reads the manager's event channel in this process; drain
	// it so services never block on it.
	s.ctx = ctx
	drainCtx, stopDrain := context.WithCancel(ctx)
	defer stopDrain()
	go func() {
		for {
			select {
			case <-s.opts.Manager.Updates():
			case <-drainCtx.Done():
				return
			}
		}
	}()
	defer s.shutdown()
	return s.mcp.Run(ctx, t)
}

// Connect serves one session over t without blocking, for tests.
func (s *Server) Connect(ctx context.Context, t mcp.Transport) (*mcp.ServerSession, error) {
	s.ctx = ctx
	go func() {
		for {
			select {
			case <-s.opts.Manager.Updates():
			case <-ctx.Done():
				return
			}
		}
	}()
	return s.mcp.Connect(ctx, t, nil)
}

func (s *Server) lifetime() context.Context { return s.ctx }

func (s *Server) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if s.opts.Runbooks != nil {
		s.opts.Runbooks.StopAll()
	}
	s.opts.Manager.StopAll(ctx)
}

// ── policy ───────────────────────────────────────────────────────────────────

// allowed reports whether agents may see svc at all.
func (s *Server) allowed(sum service.Summary) bool {
	if len(s.policy.Allow) == 0 {
		return true
	}
	for _, pattern := range s.policy.Allow {
		if ok, _ := path.Match(pattern, sum.Name); ok {
			return true
		}
	}
	return false
}

func summarize(svc service.Service) service.Summary {
	return service.Summarize(svc)
}

// find returns the allowed service called name.
func (s *Server) find(name string) (service.Service, service.Summary, error) {
	var visible []string
	for _, svc := range s.opts.Manager.Services() {
		sum := summarize(svc)
		if !s.allowed(sum) {
			continue
		}
		if sum.Name == name {
			return svc, sum, nil
		}
		visible = append(visible, sum.Name)
	}
	return nil, service.Summary{}, fmt.Errorf("no service %q (call list_services; known: %s)", name, strings.Join(visible, ", "))
}

// inputRequestsSince is the first protocol version in which a server asks for
// input by returning it from the tool call (SEP-2322) instead of sending an
// elicitation request in the middle of it.
const inputRequestsSince = "2026-07-28"

// confirm asks the user, through the client, to allow what message
// describes. It returns (nil, nil) once the user said yes; a non-nil result is
// the input request to return to the client, which asks the user and calls
// the tool again with the answer; an error means no. A client that cannot ask
// means no.
func confirm(ctx context.Context, req *mcp.CallToolRequest, key, message string) (*mcp.CallToolResult, error) {
	// The retry of a call that asked: the answer is in the request.
	if resp, ok := req.Params.InputResponses[key]; ok {
		if r, ok := resp.(*mcp.ElicitResult); ok && r.Action == "accept" && r.Content["confirm"] == true {
			return nil, nil
		}
		return nil, errors.New("the user did not confirm")
	}
	if caps := req.ClientCapabilities(); caps == nil || caps.Elicitation == nil {
		return nil, errors.New("this needs the user's confirmation, and this MCP client cannot ask for it — do it in den's TUI instead")
	}
	params := &mcp.ElicitParams{
		Message: message,
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"confirm": map[string]any{"type": "boolean", "description": "Allow it"},
			},
			"required": []string{"confirm"},
		},
	}
	if req.ProtocolVersion() >= inputRequestsSince {
		return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{key: params}}, nil
	}
	res, err := req.Session.Elicit(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("asking for confirmation: %w", err)
	}
	if res.Action != "accept" || res.Content["confirm"] != true {
		return nil, errors.New("the user did not confirm")
	}
	return nil, nil
}

// ── redaction ────────────────────────────────────────────────────────────────

var (
	signedParam = regexp.MustCompile(`(?i)(X-Amz-(Signature|Credential|Security-Token)=)[^&\s"']+`)
	longSecret  = regexp.MustCompile(`[A-Za-z0-9+/=_%.-]{64,}`)
)

// redact removes anything credential-like from a log line before an agent
// sees it: the service's current token, signed-URL parameters, and long
// opaque strings. It also drops colour codes: they are for a terminal, and an
// agent would read them as noise (they can split a secret from its match too).
func redact(line, token string) string {
	line = ansi.Strip(line)
	if token != "" {
		line = strings.ReplaceAll(line, token, "[redacted]")
	}
	line = signedParam.ReplaceAllString(line, "${1}[redacted]")
	return longSecret.ReplaceAllString(line, "[redacted]")
}

// credentialHint says how a client authenticates to a connected service,
// without the credential itself.
func credentialHint(svc service.Service, sum service.Summary) string {
	switch sum.Type {
	case service.TypeRDS, service.TypeRedshift:
		if strings.HasPrefix(sum.ConnectCommand, "mysql") {
			return "the IAM token is in the option file the connect command reads"
		}
		return "the IAM token is written to " + pgpass.Path() + " when update_pgpass is on; otherwise it is only available in den's TUI (detail view, Y)"
	case service.TypeRedis:
		if service.HoldsToken(svc) && svc.State().Token != "" {
			return "IAM auth: the token is withheld from agents — copy the full command from den's TUI (detail view, Y)"
		}
		return "no credential needed"
	case service.TypeDocumentDB:
		return "MONGODB-AWS: mongosh signs with the AWS profile in the command"
	case service.TypeOpenSearch, service.TypeNeptune:
		return "none: den's local proxy signs every request"
	}
	return "see the connect command"
}
