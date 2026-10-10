# Services

What every data store entry has in common

## What it is

A service is one data store in the Connect list: a database, a cache, a search domain, a graph or a local Compose stack. Each `services:` entry has a `type` and a section of that type's keys; the stores reached through a tunnel share the keys described here. The topic of each type has the rest.

## What you can do

- Connect a store and get it on `127.0.0.1:<local_port>`, with the credential it accepts minted for you and renewed while connected.
- Open a service's detail view: its status, local port, the connect command, the credential's time left, the log, and terminal tabs with the client already logged in.
- Copy the command that connects a client, and for stores with a token or password, the same command with it inlined.
- Reopen the forward by itself when it drops, for example on the SSM idle timeout (`reconnect`, or the toggle in the detail view), retrying after 5 seconds and backing off to a minute.
- Filter the Connect list as you type, by name, type or environment.
- Connect stores from scripts and AI agents too: `den mcp` exposes the same services.
- Keys: press `h` in the Connect panel or a detail view.

## Before you start

- What the service's transport needs: for the default SSM, the AWS CLI v2 and the Session Manager plugin. `den doctor` lists everything your config needs.
- A login for the environment's AWS profiles.
- The client of each type you use (`psql`, `redis-cli`, `mongosh`, `curl`); its topic names it.

## Configure

Each entry of `services:`:

- `name` (required): shown in den; it can repeat across environments.
- `type` (required): `rds`, `redis`, `documentdb`, `redshift`, `opensearch`, `neptune` or `docker`.
- `env`: the environment to reach it through, which supplies the connection keys below.
- A section named after the type (`rds:`, `redis:`, …) with that type's keys and these shared ones:
- `local_port`: where the store appears on `127.0.0.1`; each type has a default, and two services connected at once need different ports.
- `reconnect` (default `false`): reopen the forward when it drops. Credential renewal does not depend on it.
- `region`, `environment`, `ec2_instance_id`, `aws_profile`, `credential_profile`, `aws_region_code`, `transport`: the environment's keys, set on the service to override them or to do without `env`.

Two stores of one environment, on their own ports, both reopened after a drop:

```yaml
services:
  - name: orders dev
    type: rds
    env: dev
    rds:
      rds_host: orders.cluster-abc123.eu-central-1.rds.amazonaws.com
      local_port: 50432
      db_name: orders
      db_user: app_iam
      reconnect: true
  - name: sessions dev
    type: redis
    env: dev
    redis:
      redis_host: master.sessions-dev.abc123.euc1.cache.amazonaws.com
      local_port: 16380
      reconnect: true
```

The same store in two environments: the name repeats, the environment and the port tell them apart.

```yaml
services:
  - name: search
    type: opensearch
    env: dev
    opensearch:
      opensearch_host: vpc-search-dev-abc123.eu-central-1.es.amazonaws.com
      local_port: 59201
  - name: search
    type: opensearch
    env: prod
    opensearch:
      opensearch_host: vpc-search-prod-def456.eu-central-1.es.amazonaws.com
      local_port: 59202
```

## Limitations

- Kafka (Amazon MSK) is planned, not implemented: a `kafka` service is accepted but connecting it says so.
- Reconnect restores the forward, not your client's session: reconnect the client yourself.
- A forward carries one host and port: stores that redirect clients to other nodes (multi-shard caches, replica-set discovery) only work for the node den forwards to.
- Copying uses the system clipboard; over SSH, or without `xclip` or `wl-copy`, den asks the terminal to set it (OSC 52), which most terminals accept and tmux needs `set-clipboard on` for.
- Two services on the same `local_port` collide when both are connected; `den doctor` warns about it.

## When it goes wrong

- `Your session timed out due to inactivity` in the log: SSM closed an idle forward; set `reconnect: true` or connect again.
- The service fails at once with an AWS error: the login for `aws_profile` or `credential_profile` has expired; log in from the AWS SSO panel.
- The forward is up but the client cannot log in: the credential is fine and the store refused it; check the grants in the type's topic.

## See also

- Environments
- Transports
- Terminal tabs
- RDS and Aurora
- AI agents (MCP)
