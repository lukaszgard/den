# den with AI agents

## What it is

`den mcp` serves den to AI agents over the [Model Context Protocol](https://modelcontextprotocol.io).
Register it with Claude Code, Codex, Cursor or any MCP client, and the agent can list
your services, open a tunnel, read its status and logs, write runbooks the way den
expects them, and — when you allow it — run runbooks. The agent then queries the database with its own tools through
`localhost`, exactly as you would.

| Tool | Does |
|---|---|
| `list_services` | services with type, environment, status, local port, connect command, production flag |
| `service_status` | one service's current state |
| `connect` | opens the tunnel, waits until usable, returns `localhost`, the port and **how** the client authenticates |
| `disconnect` | closes it |
| `service_logs` | recent tunnel log lines, credential-like strings removed |
| `runbook_guide` | how to write a den runbook, plus this den's runbooks directories, environments (AWS profile, region, production) and taken names; read-only |
| `validate_runbook` | loads a runbook folder as den will and lists what is wrong; read-only, folders inside the runbooks directories only |
| `list_runbooks`, `run_runbook` | only with `mcp.allow_runbooks: true` |

It also offers the prompt `new_runbook` (in Claude Code: `/mcp__den__new_runbook <what
it should do>`), which hands the agent the guide and your request. See
[Write one with an agent](../runbooks/README.md#write-one-with-an-agent).

### The boundary

- **Credentials never leave den.** `connect` says how the client authenticates
  (`~/.pgpass`, the MySQL option file, the AWS profile, the signing proxy) and
  never returns a token. Logs are redacted. A Redis cache using IAM auth therefore
  cannot be used by the agent directly: its token is withheld.
- **Production is off by default.** With `allow_production: true`, each
  production connect still asks you in the MCP client. A client that cannot ask
  is refused.
- **Runbooks are off by default.** With `allow_runbooks: true`, a runbook marked
  `confirm: true` asks you first and shows its command. Writing one is not
  running it: `runbook_guide` and `validate_runbook` only read, and the agent
  writes the files with its own tools, which ask you as usual.
- **Runbooks from [sources](../runbook-sources/README.md) need a second opt-in.**
  They are someone else's code, and their descriptions and READMEs would reach the
  agent as text it might act on: they stay hidden, with the sequences that run one,
  unless `allow_source_runbooks: true`, and even then every run asks you first.
- **AWS SSO logins and VPNs** need a browser and stay in den's TUI.
- **Tunnels end with the session**: when the agent disconnects, den closes
  everything it opened.

## Configuration

```yaml
mcp:
  allow: ["*-int", "*-e2e"]   # service names agents may see (* and ? wildcards); all when omitted
  allow_production: false     # true: production connects are possible, each confirmed by you
  allow_runbooks: false       # true: list_runbooks and run_runbook exist
  allow_source_runbooks: false  # true: agents also see runbooks from runbooks.sources, each run confirmed by you
```

Production means the service's `environment` is `prod`.

## Prerequisites

- den and a `den.yaml`; everything a normal connect needs (see `den doctor`).
- An MCP client. Confirmation prompts need a client with elicitation support;
  without it, production connects and confirmed runbooks are refused.

## Usage

```bash
claude mcp add den -- den mcp -c ~/.config/den/den.yaml
```

Then, in Claude Code: *"connect to the int orders database and count yesterday's
orders"* — the agent calls `list_services`, `connect`, runs `psql` with the
returned command, and `disconnect`s when done.

Codex: `codex mcp add den -- den mcp -c ~/.config/den/den.yaml`. Other clients take
the same command in their MCP server settings: `command: den`,
`args: ["mcp", "-c", "/path/to/den.yaml"]`. The server is started by the client:
`den mcp` is not piped into the agent.

Everything den logs goes to stderr; stdout carries only the protocol.

### Agent Skill

Release archives include `skills/den/SKILL.md`, an [Agent Skill](https://docs.claude.com/en/docs/claude-code/skills)
that teaches an agent how to use these tools and den's JSON CLI, and the rules it
must keep (production, credentials, runbooks), with `skills/den/runbooks.md`, the
runbook guide `runbook_guide` serves. Install them for Claude Code with:

```bash
mkdir -p ~/.claude/skills/den && cp skills/den/SKILL.md skills/den/runbooks.md ~/.claude/skills/den/
```
