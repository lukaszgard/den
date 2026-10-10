# Introduction

den is a terminal dashboard that reaches the private data stores of your cloud accounts. It
opens the tunnel, mints the short-lived IAM credential the store accepts, keeps both alive
and gives you a ready `psql`, `redis-cli` or `mongosh` prompt. It is for engineers who open
a database in a private network every day and are tired of doing it by hand.

Around the tunnels it wraps the chores that come with them: AWS SSO logins, a FortiGate VPN,
secrets, and your own scripts as runbooks. It also serves itself to AI agents, so an agent
can open a tunnel without ever seeing a credential.

## What makes den different

- **One tunnel model for every store.** RDS, ElastiCache, MemoryDB, DocumentDB, Redshift,
  OpenSearch and Neptune are opened, refreshed and reconnected by the same code, whichever
  [transport](../transports/README.md) carries the traffic.
- **Credentials never leave den.** What `den list --json` and `den mcp` report has no token
  field, and logs are redacted. See the [security model](../security/README.md).
- **One `den.yaml` for the team.** A bastion is written once as an
  [environment](../environments/README.md); services, runbooks and the editor's JSON Schema
  all read the same file.

## What it looks like

A service in `den.yaml`, and the same service from the command line:

```yaml
environments:
  eu-dev:
    ec2_instance_id: "i-0123456789abcdef0"
    aws_profile: "acme-dev"
    aws_region_code: "eu-central-1"

services:
  - name: "EU DEV Orders DB"
    type: rds
    env: eu-dev
    rds:
      rds_host: "db1.abcdefghijkl.eu-central-1.rds.amazonaws.com"
      db_user: "app"
      local_port: 50432
```

```text
$ den list
  EU DEV Orders DB                [rds       ]  idle
```

Run `den` for the dashboard, press `c` on the service and den opens the tunnel; `t` runs
`psql` next to the logs, already logged in.

## Start here

- [Why den?](../why-den/README.md): the problem it solves, and what it does not try to be.
- [Quickstart](../quickstart/README.md): from a fresh machine to a `psql` prompt.
- [How den works](../concepts/README.md): services, environments, transports and tunnels in one page.
- [den with AI agents](../mcp/README.md): `den mcp` and runbooks written by an agent.

Questions and bugs go to the [issue tracker](https://github.com/lukaszgard/den/issues).
