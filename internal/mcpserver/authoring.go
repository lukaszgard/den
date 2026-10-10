package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lukaszgard/den/internal/runbook"
)

// Writing a runbook is the agent's own work, with its own file tools, which
// ask the user as they always do. What den adds is knowledge: how a runbook
// must look, this den's environments and directories, and a check that loads
// the folder exactly as den will. Both tools only read, and hand out no
// credential, so they are there whatever the mcp: block says.

type (
	guideInput struct {
		Folder string `json:"folder,omitempty" jsonschema:"the runbook's folder when there is one: a name in den's first runbooks directory, or a path in one"`
	}
	guideOutput struct {
		Guide string `json:"guide"`
	}
	validateInput struct {
		Folder string `json:"folder" jsonschema:"the runbook's folder: a name in den's first runbooks directory, or a path in one"`
	}
)

func (s *Server) addAuthoring() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "runbook_guide",
		Description: "How to write a den runbook: the folder and runbook.yaml format, how den runs it and what it sets " +
			"in its environment, the rules, notes per language (bash, Python, Go, Rust, Node), and this den's runbooks " +
			"directories, environments (AWS profile, region, production) and names already taken. Read it before writing one.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, s.runbookGuide)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "validate_runbook",
		Description: "Load a runbook folder the way den will and report what keeps it from loading or running (errors) " +
			"and what is worth fixing (warnings). Call it after writing or changing a runbook, until ok is true.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, s.validateRunbook)
	s.mcp.AddPrompt(&mcp.Prompt{
		Name:        "new_runbook",
		Title:       "New den runbook",
		Description: "Write a new den runbook from a description of what it should do.",
		Arguments: []*mcp.PromptArgument{
			{Name: "request", Description: "What it should do, against which environment, in which language", Required: true},
			{Name: "name", Description: "Its name, when you have one in mind"},
		},
	}, s.newRunbookPrompt)
}

func (s *Server) registry() (*runbook.Registry, error) {
	if s.opts.Runbooks == nil {
		return nil, errors.New("den has no runbook registry in this session")
	}
	return s.opts.Runbooks, nil
}

func (s *Server) runbookGuide(_ context.Context, _ *mcp.CallToolRequest, in guideInput) (*mcp.CallToolResult, guideOutput, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, guideOutput{}, err
	}
	target := ""
	if in.Folder != "" {
		if target, err = reg.ResolveFolder(in.Folder); err != nil {
			return nil, guideOutput{}, err
		}
	}
	return nil, guideOutput{Guide: reg.Guide(target)}, nil
}

func (s *Server) validateRunbook(_ context.Context, _ *mcp.CallToolRequest, in validateInput) (*mcp.CallToolResult, runbook.Validation, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, runbook.Validation{}, err
	}
	dir, err := reg.ResolveFolder(in.Folder)
	if err != nil {
		return nil, runbook.Validation{}, err
	}
	v := reg.ValidateFolder(dir)
	if v.OK {
		// So list_runbooks and run_runbook know it at once.
		reg.Rescan()
	}
	return nil, v, nil
}

func (s *Server) newRunbookPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	request := strings.TrimSpace(req.Params.Arguments["request"])
	if request == "" {
		return nil, errors.New("say what the runbook should do")
	}
	var b strings.Builder
	b.WriteString(reg.Guide(""))
	b.WriteString("\n\n## The request\n\n")
	b.WriteString(request)
	if name := strings.TrimSpace(req.Params.Arguments["name"]); name != "" {
		fmt.Fprintf(&b, "\n\nName it %q.", name)
	}
	b.WriteString(`

## What to do

1. Pick the environment and the language the request asks for; ask the user when it is unclear.
2. Create a folder for it in den's first runbooks directory, named after the runbook in
   lower case with dashes, and write runbook.yaml, the code and README.md in it.
3. Call validate_runbook with the folder, fix what it reports, and call it again until ok is true.
4. Tell the user how to run it: den's Runbooks panel (select it, r) or den run "<name>".`)
	return &mcp.GetPromptResult{
		Description: "Write a new den runbook",
		Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: b.String()}}},
	}, nil
}
