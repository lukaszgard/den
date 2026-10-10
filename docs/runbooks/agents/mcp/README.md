# From your agent

## What it is

Register `den mcp` with your agent once. From then on the agent has den's tools —
`runbook_guide`, `validate_runbook`, and, when you allow it, `run_runbook` — and a
prompt, `/mcp__den__new_runbook`, that hands it the guide and your request. `den mcp` is
a server the agent starts itself: it is registered, not piped into the agent.

## Configuration

[`den.yaml`](den.yaml) is the CLI demo's config plus:

| Key | Meaning |
|---|---|
| `mcp.allow_runbooks` | `true`: the agent may also list and run runbooks, so it can run the one it wrote. Writing needs no permission: the guide and the validator only read |

## Prerequisites

Claude Code (or Codex), logged in, and `go` for the runbook the demo asks for. No AWS.

## Usage

```bash
claude mcp add den -- den mcp -c ~/.config/den/den.yaml      # Claude Code
codex mcp add den -- den mcp -c ~/.config/den/den.yaml       # Codex
```

Then, in the agent: `/mcp__den__new_runbook the current UTC time and this env's region,
in Go` (Claude Code), or the same in plain words. The recording registers den for its
folder only and removes it at the end (`claude mcp remove den`). Re-render:
`vhs docs/runbooks/agents/mcp/agent-mcp.tape`.
