# Glossary

The words den uses, in one line each, with the page that explains them.

**Bastion.** The EC2 instance inside the private network that a tunnel goes through: den's
SSM transport forwards through it (`ec2_instance_id`), and an `ssh` transport uses a bastion
host. See [Environments](../environments/README.md).

**`den.lock`.** The file beside `den.yaml` that pins each runbook source to a commit.
`den runbook source sync` checks out exactly those commits. See
[Runbook sources](../runbook-sources/README.md).

**Engine.** Two things share the word. In den's code, the part of a tunnel that is specific to
a store: where to forward, which credential to mint and how the user connects. In `den.yaml`,
the `engine:` key of an RDS service, `postgres` or `mysql`, which picks the port the token is
signed for, the client and where the token goes. See [RDS](../rds/README.md).

**Environment.** A named set of connection settings (bastion, profiles, region) under
`environments:`, which services and runbooks refer to with `env:`. See
[Environments](../environments/README.md).

**Health check.** A probe that proves traffic reaches the far side, rather than that something
listens locally. For a tunnel, a `host:port` dialled through its SOCKS proxy; for a VPN, a
`host:port` reachable only over it. See [Tunnel](../tunnel/README.md) and [VPN](../vpn/README.md).

**IAM token.** The short-lived credential den mints for a data store in place of a password
(15 minutes for RDS, ElastiCache and MemoryDB). See [Security model](../security/README.md).

**MCP.** The Model Context Protocol. `den mcp` serves den to AI agents over it, so an agent can
list services and open tunnels without ever seeing a credential. See [den mcp](../mcp/README.md).

**Pane (panel).** One of the screens in den's left menu: Connect, Tunnel, Runbooks, AWS, VPN,
Secrets, Create Env, Config, Help, Updates and Logs. `h` or `?` lists the keys of the one you are
in. See [Shortcuts](../shortcuts/README.md).

**Runbook.** A script of yours, in a folder with a `runbook.yaml`, that den runs with your AWS
profile, region and live tunnel ports (`DEN_PORT_*`) already set. See [Runbooks](../runbooks/README.md).

**Sequence.** Named runbooks run in order, stopping at the first failure. See
[Runbooks](../runbooks/README.md).

**Service.** Anything you connect from the Connect panel: a data store reached through a tunnel,
or a Docker Compose stack. Declared under `services:`. See [Configuration reference](../config/README.md).

**Source.** A git repository of runbook folders, declared under `runbooks.sources`, whose
runbooks appear as `<source>/<name>`. See [Runbook sources](../runbook-sources/README.md).

**SSO session.** An `[sso-session X]` section of `~/.aws/config`. Logging in to it authenticates
every profile that uses it. See [AWS SSO](../aws/README.md).

**Token refresh.** den re-mints an IAM token before it expires (every 13 minutes for a 15-minute
token) and writes it where the client reads it. `r` mints one now. See [RDS](../rds/README.md).

**Transport.** How den reaches the private network a service lives in: `ssm` (the default, a
port-forward through the bastion), `ssh`, `eice` (an EC2 Instance Connect Endpoint) or `kubectl`.
See [Transports](../transports/README.md).

**Trust.** den's memory of which runbooks from a source you have run, by the git tree of their
folder. A new or changed runbook asks before it runs. See [Runbook sources](../runbook-sources/README.md).

**Tunnel.** A local port that forwards to a private host. Every data store service is one, opened
with an SSM port-forward and kept alive by den (readiness check, credential refresh, reconnect).
`tunnels:` in `den.yaml` is the other kind, an SSH or SOCKS command you write. See
[Transports](../transports/README.md) and [Tunnel](../tunnel/README.md).
