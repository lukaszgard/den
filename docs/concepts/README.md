# How den works

The few ideas the rest of the documentation builds on. The [glossary](../glossary/README.md)
has the shorter definitions.

## `den.yaml`

One YAML file describes everything den shows: environments, services, tunnels, VPNs, secrets
and runbooks. den reads `-c <path>`, then `./den.yaml`, then `den.yaml` in the config directory (`~/.config/den`; see [Files and folders](../files/README.md)). Every
key is in the [configuration reference](../config/README.md), and
[`den schema`](../schema/README.md) gives your editor completion and validation. The file
holds references (profiles, instance IDs, secret paths), never secret values, so it can be
committed.

## Service

A **service** is anything you connect from the Connect panel: a data store reached through a
tunnel, or a Docker Compose stack. It has a type (`rds`, `redis`, `documentdb`, `redshift`,
`opensearch`, `neptune`, `docker`), a state (idle, connecting, connected, reconnecting, error) and, once
connected, a local port. The [data store pages](../rds/README.md) say what each type needs.

## Environment

An **environment** says how den gets into one network: the bastion, the AWS profile that
opens the tunnel, the profile that mints the credential, and the region. Services and
runbooks point at it with `env:`, so a new bastion is one edit. See
[Environments](../environments/README.md).

## Transport and tunnel

A **tunnel** is a local port that forwards to a private host. A **transport** is how den
reaches that network: an SSM port-forward through the bastion (the default), SSH, an EC2
Instance Connect Endpoint or `kubectl port-forward`. Every data store goes through the same
tunnel lifecycle, whichever transport carries it: wait for the port, mint the credential,
monitor the forward, reconnect. See [Transports](../transports/README.md). The `tunnels:` key
is the other kind, an SSH or SOCKS command you write yourself
([SSH & SOCKS tunnels](../tunnel/README.md)).

## Credential refresh

A store accepts a short-lived IAM credential instead of a password: an authentication token
for RDS, ElastiCache and MemoryDB (15 minutes), temporary database credentials for Redshift,
and your AWS session for DocumentDB, OpenSearch and Neptune. den mints it once the port is
ready, writes it where the client looks (`~/.pgpass`, an option file) and renews it before it
expires, 13 minutes into a 15-minute token. `r` mints one now. See the
[security model](../security/README.md#where-credentials-are-written).

## The panels

`den` opens a dashboard with a menu on the left: Connect, Tunnel, Runbooks, AWS, VPN, Secrets,
Create Env, Config, Help, Updates and Logs. `h` or `?` lists the keys that work where you are
([Keyboard shortcuts](../shortcuts/README.md)). The same services are reachable without the
dashboard through `den connect`, `den list` and `den run`, and by an AI agent through
[`den mcp`](../mcp/README.md).
