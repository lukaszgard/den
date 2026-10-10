package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/service"
)

type (
	noInput struct{}

	nameInput struct {
		Name string `json:"name" jsonschema:"the service name, exactly as list_services reports it"`
	}

	logsInput struct {
		Name  string `json:"name" jsonschema:"the service name, exactly as list_services reports it"`
		Lines int    `json:"lines,omitempty" jsonschema:"how many recent lines, 50 by default, at most 200"`
	}

	runInput struct {
		Name   string            `json:"name" jsonschema:"the runbook or sequence name, as list_runbooks reports it"`
		Params map[string]string `json:"params,omitempty" jsonschema:"parameter values by name"`
	}

	servicesOutput struct {
		Services []service.Summary `json:"services"`
		// Hidden counts services den.yaml's mcp.allow keeps from agents.
		Hidden int `json:"hidden,omitempty"`
	}

	connectOutput struct {
		Service service.Summary `json:"service"`
		Host    string          `json:"host"`
		Port    int             `json:"port"`
		// Credential says how a client authenticates; it never holds one.
		Credential string `json:"credential"`
	}

	logsOutput struct {
		Lines []string `json:"lines"`
	}

	runbookInfo struct {
		Name        string             `json:"name"`
		Kind        string             `json:"kind"`
		Description string             `json:"description,omitempty"`
		NeedsOK     bool               `json:"needs_confirmation"`
		Params      []config.ParamSpec `json:"params,omitempty"`
		Steps       []string           `json:"steps,omitempty"`
		// Source is the runbook source it comes from: someone else's code.
		Source string `json:"source,omitempty"`
	}

	runbooksOutput struct {
		Runbooks []runbookInfo `json:"runbooks"`
	}

	runOutput struct {
		Status   string   `json:"status"`
		ExitCode int      `json:"exit_code"`
		Output   []string `json:"output"`
	}
)

func (s *Server) addTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_services",
		Description: "List the services den can connect: type, environment, status, local port, connect command, and whether it is production.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, s.listServices)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "service_status",
		Description: "Show one service's current status, local port and connect command.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, s.serviceStatus)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "connect",
		Description: "Open the tunnel to a service and wait until it is usable. Returns localhost and the port " +
			"to use and how the client authenticates. Production services need the user's confirmation.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, s.connect)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "disconnect",
		Description: "Close a service's tunnel.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: new(false)},
	}, s.disconnect)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "service_logs",
		Description: "Recent log lines of a service's tunnel, with anything credential-like removed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, s.serviceLogs)
	s.addAuthoring()
	if s.policy.AllowRunbooks {
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "list_runbooks",
			Description: "List the runbooks and sequences den knows (den.yaml and the runbook folders), with their parameters.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		}, s.listRunbooks)
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name: "run_runbook",
			Description: "Run a runbook or sequence to completion and return its exit status and output. " +
				"Runbooks marked for confirmation ask the user first.",
			Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(true)},
		}, s.runRunbook)
	}
}

func (s *Server) listServices(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, servicesOutput, error) {
	out := servicesOutput{Services: []service.Summary{}}
	for _, svc := range s.opts.Manager.Services() {
		sum := summarize(svc)
		if !s.allowed(sum) {
			out.Hidden++
			continue
		}
		out.Services = append(out.Services, sum)
	}
	return nil, out, nil
}

func (s *Server) serviceStatus(_ context.Context, _ *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, service.Summary, error) {
	_, sum, err := s.find(in.Name)
	return nil, sum, err
}

func (s *Server) connect(ctx context.Context, req *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, connectOutput, error) {
	svc, sum, err := s.find(in.Name)
	if err != nil {
		return nil, connectOutput{}, err
	}
	if sum.Type == service.TypeAWS || sum.Type == service.TypeVPN {
		return nil, connectOutput{}, fmt.Errorf("%s needs a browser login: connect it from den's TUI", sum.Name)
	}
	if sum.Production {
		if !s.policy.AllowProduction {
			return nil, connectOutput{}, fmt.Errorf("%s is a production service, and den.yaml's mcp.allow_production is off", sum.Name)
		}
		msg := fmt.Sprintf("An AI agent wants to open a tunnel to the PRODUCTION %s service %q. Allow it?", sum.Type, sum.Name)
		if ask, err := confirm(ctx, req, "confirm_production", msg); ask != nil || err != nil {
			return ask, connectOutput{}, err
		}
	}

	switch svc.State().Status {
	case service.StatusConnected:
	case service.StatusConnecting, service.StatusReconnecting:
		// Already on its way up: wait for it below.
	default:
		// The tunnel outlives this call, so it runs under the server's context.
		if err := s.opts.Manager.StartOne(s.lifetime(), svc); err != nil && !errors.Is(err, service.ErrAlreadyRunning) {
			return nil, connectOutput{}, err
		}
	}
	if err := waitConnected(ctx, svc, s.opts.ConnectTimeout); err != nil {
		return nil, connectOutput{}, err
	}
	sum = summarize(svc)
	return nil, connectOutput{Service: sum, Host: "localhost", Port: sum.LocalPort, Credential: credentialHint(svc, sum)}, nil
}

// waitConnected polls svc until it is connected, fails, or timeout passes.
func waitConnected(ctx context.Context, svc service.Service, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st := svc.State()
		switch st.Status {
		case service.StatusConnected:
			return nil
		case service.StatusError:
			return fmt.Errorf("connect failed: %v", st.Error)
		case service.StatusIdle:
			return errors.New("the service stopped before connecting")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("still %s after %s; check service_logs", st.Status, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Server) disconnect(ctx context.Context, _ *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, service.Summary, error) {
	svc, _, err := s.find(in.Name)
	if err != nil {
		return nil, service.Summary{}, err
	}
	if err := svc.Stop(ctx); err != nil {
		return nil, service.Summary{}, err
	}
	return nil, summarize(svc), nil
}

func (s *Server) serviceLogs(_ context.Context, _ *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, logsOutput, error) {
	svc, _, err := s.find(in.Name)
	if err != nil {
		return nil, logsOutput{}, err
	}
	n := in.Lines
	if n <= 0 {
		n = 50
	}
	n = min(n, 200)
	logs := svc.Logs()
	if len(logs) > n {
		logs = logs[len(logs)-n:]
	}
	token := svc.State().Token
	out := logsOutput{Lines: make([]string, len(logs))}
	for i, l := range logs {
		out.Lines[i] = redact(l, token)
	}
	return nil, out, nil
}

func (s *Server) listRunbooks(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, runbooksOutput, error) {
	out := runbooksOutput{Runbooks: []runbookInfo{}}
	if s.opts.Runbooks == nil {
		return nil, out, nil
	}
	for _, r := range s.opts.Runbooks.Runbooks() {
		rb := r.Runbook()
		if s.hidden(rb) {
			continue
		}
		info := runbookInfo{Name: rb.Name, Kind: "runbook", Description: rb.Description, NeedsOK: rb.Confirm, Params: rb.Params}
		if rb.Remote != nil {
			info.Source, info.NeedsOK = rb.Remote.Source, true
		}
		out.Runbooks = append(out.Runbooks, info)
	}
	for _, sq := range s.opts.Runbooks.Sequences() {
		seq := sq.Sequence()
		remote, hidden := s.sequenceSources(seq)
		if hidden {
			continue
		}
		out.Runbooks = append(out.Runbooks, runbookInfo{Name: seq.Name, Kind: "sequence", Description: seq.Description,
			NeedsOK: sq.NeedsConfirm() || remote, Params: sq.Params(), Steps: seq.Steps})
	}
	return nil, out, nil
}

// hidden reports a runbook agents may not see: one from a runbook source,
// unless den.yaml's mcp.allow_source_runbooks lets them. Its code is someone
// else's, and its description and README would reach the agent as text it
// might follow.
func (s *Server) hidden(rb runbook.Runbook) bool {
	return rb.Remote != nil && !s.policy.AllowSourceRunbooks
}

// sequenceSources reports whether a sequence runs a runbook from a source,
// and whether that keeps it from agents.
func (s *Server) sequenceSources(seq runbook.Sequence) (remote, hidden bool) {
	for _, step := range seq.Steps {
		if r, ok := s.opts.Runbooks.Lookup(step); ok && r.Runbook().Remote != nil {
			remote = true
			hidden = hidden || s.hidden(r.Runbook())
		}
	}
	return remote, hidden
}

const errSourceRunbook = "%q runs code from a runbook source: den.yaml's mcp.allow_source_runbooks lets agents run those, each time after the user confirms"

func (s *Server) runRunbook(ctx context.Context, req *mcp.CallToolRequest, in runInput) (*mcp.CallToolResult, runOutput, error) {
	reg := s.opts.Runbooks
	if reg == nil {
		return nil, runOutput{}, errors.New("no runbooks are configured")
	}
	if r, ok := reg.Lookup(in.Name); ok {
		rb := r.Runbook()
		if missing := runbook.MissingParams(rb.Params, in.Params); len(missing) > 0 {
			return nil, runOutput{}, fmt.Errorf("missing required parameter(s): %s", strings.Join(missing, ", "))
		}
		if s.hidden(rb) {
			return nil, runOutput{}, fmt.Errorf(errSourceRunbook, rb.Name)
		}
		// A source's runbook asks every time: the user's yes in the TUI was
		// to running it themselves, not to an agent running it.
		if rb.Remote != nil {
			msg := fmt.Sprintf("An AI agent wants to run %q, a runbook from the runbook source %s (%s):\n%s\nAllow it?",
				rb.Name, rb.Remote.Source, rb.Origin(), rb.Command)
			if ask, err := confirm(ctx, req, "confirm_runbook", msg); ask != nil || err != nil {
				return ask, runOutput{}, err
			}
		} else if rb.Confirm {
			msg := fmt.Sprintf("An AI agent wants to run the runbook %q:\n%s\nAllow it?", rb.Name, rb.Command)
			if ask, err := confirm(ctx, req, "confirm_runbook", msg); ask != nil || err != nil {
				return ask, runOutput{}, err
			}
		}
		opts := reg.ExecOptionsFor(rb, in.Params)
		opts.Origin = runbook.OriginMCP
		result := r.Execute(ctx, opts)
		return nil, runResult(result, opts.Redactor), nil
	}
	if sq, ok := reg.LookupSequence(in.Name); ok {
		if missing := runbook.MissingParams(sq.Params(), in.Params); len(missing) > 0 {
			return nil, runOutput{}, fmt.Errorf("missing required parameter(s): %s", strings.Join(missing, ", "))
		}
		remote, hidden := s.sequenceSources(sq.Sequence())
		if hidden {
			return nil, runOutput{}, fmt.Errorf(errSourceRunbook, in.Name)
		}
		if sq.NeedsConfirm() || remote {
			msg := fmt.Sprintf("An AI agent wants to run the sequence %q (%s). Allow it?", in.Name, strings.Join(sq.Sequence().Steps, " → "))
			if ask, err := confirm(ctx, req, "confirm_runbook", msg); ask != nil || err != nil {
				return ask, runOutput{}, err
			}
		}
		return nil, runResult(sq.Execute(ctx, runbook.SequenceOptions{Params: in.Params, Origin: runbook.OriginMCP}), nil), nil
	}
	return nil, runOutput{}, fmt.Errorf("no runbook or sequence %q (call list_runbooks)", in.Name)
}

func runResult(r runbook.Run, red *runbook.Redactor) runOutput {
	logs := r.Logs
	if len(logs) > 200 {
		logs = logs[len(logs)-200:]
	}
	if red != nil {
		logs = red.Lines(logs)
	}
	out := runOutput{Status: r.Status.String(), ExitCode: r.ExitCode, Output: make([]string, len(logs))}
	for i, l := range logs {
		out.Output[i] = redact(l, "")
	}
	return out
}
