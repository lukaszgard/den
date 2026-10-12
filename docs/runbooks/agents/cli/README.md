# From the CLI

## What it is

One command: den makes the runbook's folder and starts Claude Code (or another agent CLI
den can start, such as Codex or OpenCode) in it, with den's MCP server and its runbook
guide already connected. You describe the runbook
in a sentence; the agent writes it, builds it and checks it with `validate_runbook`; den
then says whether the folder loads.

## Configuration

[`den.yaml`](den.yaml) holds only what this demo needs:

| Key | Meaning |
|---|---|
| `aws.config_path`, `credentials_path` | `/dev/null`: no real profiles in a recording |
| `environments.demo-e2e` | A placeholder environment; the agent's runbook reads its profile, region and bastion from what den sets |
| `runbooks.dirs` | `runbooks`, next to this file: where the new folder is made (git-ignored) |
| `runbooks.template` | `../template`: a new folder starts as a copy. Its `.claude/settings.json` pre-allows file writes and the build **for this recording**; normally the agent asks before each |
| `runbooks.agent` | A kind such as `claude`, `codex` or `opencode`, `auto` (the first one installed) or a `runbooks.agents` name |

## Prerequisites

Claude Code, logged in (or another agent CLI den can start), and `cargo` for the Rust runbook the demo asks for.
No AWS.

## Usage

```bash
den runbook new "Env report" \
  "print AWS_PROFILE, AWS_REGION and DEN_EC2_INSTANCE_ID as a small table, in Rust" \
  --agent claude -c docs/runbooks/agents/cli/den.yaml
den run "Env report" -c docs/runbooks/agents/cli/den.yaml
```

With your own config, drop `-c`. Re-render the recording from the repository root:
`vhs docs/runbooks/agents/cli/agent-cli.tape`.
