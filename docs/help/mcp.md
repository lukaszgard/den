# AI agents (MCP)

Let Claude Code, Codex or Cursor use den, safely

## What it is

`den mcp` serves den to AI agents over the Model Context Protocol. Register it with Claude Code, Codex, Cursor or any MCP client, and the agent can list your services, open a tunnel, read its status and logs, and write runbooks the way den expects them; with your permission, run runbooks too. The agent then queries the store with its own tools through `localhost`, as you would. Credentials never leave den.

## What you can do

- Let an agent list services, connect and disconnect them, and read their status and logs.
- Have it write a runbook from a description, checked by den before you run it (the `new_runbook` prompt, or `den runbook new NAME --agent`).
- Limit which services agents see, by name with wildcards.
- Keep production off limits, or allow it with your confirmation on every connect.
- Allow agents to run runbooks; those marked `confirm` still ask you first, and runbooks from git sources need a second opt-in.
- Count on cleanup: when the agent's session ends, den closes every tunnel it opened.

## Before you start

- An MCP client. Confirmations need one that can ask you (elicitation); without it, production connects and confirmed runbooks are refused.
- Everything a normal connect needs: `den doctor` lists it.
- Register den once, for example `claude mcp add den -- den mcp -c ~/.config/den/den.yaml`, or `codex mcp add den -- den mcp -c ~/.config/den/den.yaml`.

## Configure

An `mcp:` section limits what agents may do; an `ai:` section picks the model behind `den doctor --explain`. Without `mcp:`, agents see every service, connect only non-production ones, and run no runbooks.

- `allow`: the service names agents may see, with `*` and `?`; all when left out.
- `allow_production` (default `false`): agents may connect production services, each connect confirmed by you.
- `allow_runbooks` (default `false`): agents may list and run runbooks.
- `allow_source_runbooks` (default `false`): agents also see runbooks from git sources, each run confirmed by you; needs `allow_runbooks`.
- `ai.model` (default `claude-opus-5`): the Claude model `den doctor --explain` asks.

Development and integration services only, no runbooks:

```yaml
mcp:
  allow: ["* dev", "*-int"]
```

Everything, production included with a confirmation each time, and your own runbooks:

```yaml
mcp:
  allow_production: true
  allow_runbooks: true
```

Runbooks from git sources as well, and another model for `den doctor --explain`:

```yaml
mcp:
  allow_runbooks: true
  allow_source_runbooks: true
ai:
  model: claude-sonnet-5-5
```

## Limitations

- Agents never get a token or password: `connect` says how a client authenticates (`~/.pgpass`, the MySQL option file, the AWS profile, the signing proxy). A cache with IAM auth is therefore out of an agent's reach.
- AWS SSO logins and VPNs need a browser and stay in the dashboard.
- Production means `environment: prod`; a production stage under another name is not protected.
- Tunnels end with the agent's session; they are not shared with a dashboard running at the same time.

## When it goes wrong

- A production connect is refused although `allow_production` is on: the client cannot ask you to confirm; use one with elicitation support.
- The agent sees no services: `allow` matches none of their names; patterns match whole names.
- A connect fails with an expired login: log in from the dashboard's AWS SSO panel, then let the agent try again.

## See also

- Runbooks
- Runbook sources
- Environments
- Doctor
- `docs/mcp/README.md`
