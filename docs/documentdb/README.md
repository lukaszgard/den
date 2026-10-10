# DocumentDB

Opens an SSM port-forward to an Amazon DocumentDB cluster through the bastion
host and builds the `mongosh` command that authenticates over it with AWS IAM
(the `MONGODB-AWS` mechanism) instead of a username and password.

It works like the `rds` and `redis` service types — `c` connects, `d`
disconnects, `y` copies the client command — with one structural difference:
**there is no token.**

RDS IAM auth mints a token with `generate-db-auth-token`, and ElastiCache mints a
presigned SigV4 URL; both expire and den refreshes them on a timer. DocumentDB
has no such step. The driver signs the auth handshake itself with whatever the
AWS credential provider chain resolves, and the *cluster* calls
`sts:GetCallerIdentity` to work out who you are and match that ARN against its
`$external` user list. So there is no token bar in the detail view, no `p`/`r`
key, and nothing to refresh. `rds-db:connect` is an RDS/Aurora action and has no
effect here.

## Configuration

```yaml
services:
  - name: "EU DEV Documents"
    type: documentdb
    env: eu-dev
    documentdb:
      local_port: 50141
      reconnect: true   # auto-reopen the SSM port-forward after an idle timeout / drop
      documentdb_host: docs-dev.cluster-abcdefghijkl.eu-central-1.docdb.amazonaws.com
      # db_name: readWriteDB
      # ca_file: ~/.config/den/global-bundle.pem
```

| Field             | Required | Description                                                                                     |
|-------------------|----------|-------------------------------------------------------------------------------------------------|
| `documentdb_host` | yes      | Cluster endpoint. Validated at startup — den refuses to load a config without it                |
| `local_port`      | no       | Local port for the tunnel (default `27017`)                                                     |
| `reconnect`       | no       | Auto-reopen the SSM port-forward when it drops (default `false`). See [Auto-reconnect](#auto-reconnect) |
| `db_name`         | no       | Placed in the URI path. Leave unset and select it in the shell with `use readWriteDB`            |
| `ca_file`         | no       | Path to the Amazon RDS global CA bundle. Auto-downloaded to `~/.config/den/` when unset          |

The `env:` reference supplies the rest through the normal environment merge:

| From the environment | Used for                                                            |
|----------------------|---------------------------------------------------------------------|
| `ec2_instance_id`    | SSM port-forward target (the bastion)                               |
| `aws_profile`        | Opening the SSM tunnel                                              |
| `credential_profile`    | **The identity mongosh authenticates as** — becomes `AWS_PROFILE=`  |
| `aws_region_code`    | SSM region, and picks the CN truststore for `cn-*` regions          |

> **Note on the `documentdb:` key.** It must match the service type. Nesting
> these settings under `redis:` produces a `documentdb` service with no spec;
> den rejects that at startup rather than failing later at connect time.

### Local port assignment

`local_port` is defined **per service**, at `services[].<type>.local_port`. Every
tunnel needs its own port: two services sharing one `local_port` collide at
connect time, and `den config --check` reports it. Pick a scheme that makes a port
identify a service, such as one range per type (`504xx` for DocumentDB) and one
number per environment inside it, and write it down next to the services.

### Auto-reconnect

Auto-reconnect is about the **SSM port-forward** den opens to the cluster (with
another `transport`, the ssh, EC2 Instance Connect or kubectl forward instead).

SSM Session Manager terminates a port-forwarding session after a period of
inactivity (`Your session timed out due to inactivity and has been terminated.`).
By default den leaves the service in an error state when that happens, and you
reconnect manually with `c`.

Set `reconnect: true` (or press `a` in the Connect list / detail view at runtime)
and den instead reopens the port-forward automatically:

- On drop it enters **RECONNECTING** (`↻`) and retries with exponential backoff —
  5s, then doubling, capped at 60s. The backoff resets to 5s after each successful
  reconnect.
- The setting shows in the detail header as `Auto-reconnect: ●` (on) or `○` (off);
  `a` toggles it live and the change takes effect on the next drop.
- Turning it **off** while RECONNECTING stops further attempts; `d` disconnects and
  cancels the loop entirely.

What reconnect restores is the **port-forward**, not your client session. When the SSM
session drops, your open `mongosh` connection drops with it — den reopens the local
port so you can immediately reconnect the client, but it does not re-run `mongosh`
for you. There is no token to regenerate (DocumentDB IAM auth has no token step),
so a DocumentDB reconnect is just the port-forward coming back.

The same `reconnect` key and `a` toggle exist for `rds` and `redis`; those
additionally regenerate their IAM auth token on each reconnect. Their token refresh
is separate: while the port-forward is up den renews the token every 13 minutes,
whether auto-reconnect is on or off.

### Identity — no assume-role

Developers authenticate **directly as their SSO role**
(`AWSReservedSSO_<PermissionSet>_*`, reached through the `acme-dev` profile).
There is no assume-role hop and no trust policy to maintain: the cluster's
`$external` database has that role registered as a user, granted `readWrite` on
`readWriteDB`.

Two consequences worth knowing:

- **That identity is shared** by everyone holding the same permission set.
  DocumentDB discards the session name during ARN normalization, so the audit
  log cannot tell developers apart.
- **The `_<hash>` suffix is generated by IAM Identity Center.** If the permission
  set is ever deleted and re-provisioned the ARN changes and authentication
  breaks for *everyone at once*, with no obvious cause. The fix is to re-create
  the `$external` user with the new ARN.

## Prerequisites

- **AWS CLI v2** and the **Session Manager plugin** — same as `rds`/`redis`.
- **mongosh 2.5+.** This is a hard requirement, not a recommendation: DocumentDB
  IAM auth needs the Node.js driver ≥ 6.13.1, and older mongosh builds bundle a
  driver that cannot perform `MONGODB-AWS` at all. It will fail no matter how
  correct the configuration is. den logs a warning if `mongosh` is not on `PATH`.
- **A valid SSO session:** `aws sso login --profile acme-dev`.
- **The caller's SSO role registered in `$external`** on the cluster, with a role
  on the target database. Nothing in IAM grants this — all authorization is
  database-side.
- **No IAM permissions policy is required** to connect. `sts:GetCallerIdentity`
  needs no permission and cannot be denied by policy.

## Usage

1. Launch `den`, open **Connect**, and select the `docdb` entry.
2. Press `c`. den opens the SSM tunnel, verifies the local port genuinely accepts
   connections (retrying up to 3×), downloads the CA bundle if it is not cached,
   and turns the status dot green.
3. Press `y` to copy the command, then paste it into a terminal:

```bash
AWS_PROFILE=acme-dev mongosh "mongodb://localhost:50141/?tls=true&directConnection=true&retryWrites=false" \
  --authenticationMechanism MONGODB-AWS \
  --authenticationDatabase '$external' \
  --tlsCAFile ~/.config/den/global-bundle.pem \
  --tlsAllowInvalidHostnames
```

4. Or skip the copying: in the detail view (`Enter`), `t` runs the same `mongosh`
   command in a [terminal tab](../terminal/README.md) next to the logs, with
   `AWS_PROFILE` set for it.
5. Press `d` to disconnect. It also stops the clients in the service's terminal tabs.

### Confirming you are actually authenticated

```js
db.runCommand({ connectionStatus: 1 }).authInfo
```

Expect your path-less SSO role ARN and the roles it holds:

```js
{
  authenticatedUsers: [
    { user: 'arn:aws:iam::111122223333:role/AWSReservedSSO_<PermissionSet>_<hash>', db: '$external' }
  ],
  authenticatedUserRoles: [ { role: 'readWrite', db: 'readWriteDB' } ]
}
```


An **empty** `authenticatedUsers` array means the connection succeeded but
authentication did **not** — mongosh does not always surface this loudly, so
check for it explicitly rather than trusting the prompt.

## Why the connection string looks like that

Three parameters are load-bearing and should not be "tidied up":

| Parameter                    | Why                                                                                      |
|------------------------------|------------------------------------------------------------------------------------------|
| `directConnection=true`      | **Required when tunneling** — see below                                                  |
| *(no `replicaSet=rs0`)*      | Same reason                                                                              |
| `retryWrites=false`          | DocumentDB does not support retryable writes                                              |
| `--tlsAllowInvalidHostnames` | The server certificate is issued for the cluster endpoint, not `localhost`                |
| `authSource=$external`       | IAM users live in `$external`, not `admin`. Single-quote it — `$` expands in most shells |

## Troubleshooting

**`MongoServerSelectionError: Server selection timed out`** — this is *not* an
auth error; authentication was never attempted. It means replica-set discovery
was enabled over the tunnel: the driver connects to `localhost`, runs `hello`,
receives the replica set config containing the **real in-VPC hostnames**,
discards `localhost`, and tries to reach hosts that do not resolve from your
machine. Keep `directConnection=true` and no `replicaSet=rs0`. A working session's
prompt reads `rs0 [direct: primary]` — that `[direct: ...]` marker is the
confirmation.

**Auth fails only sometimes** — almost always stale exported credentials.
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN` outrank
`AWS_PROFILE` in the provider chain, so an expired session token silently wins
over a freshly logged-in SSO profile. den logs a warning when it sees these set,
but it only sees its *own* environment — if you paste the command into a
different shell, clear them there:

```bash
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
```

**Credential resolution error** — the SSO token expired; run `aws sso login
--profile acme-dev` again. Note that an already-open mongosh session
keeps working after expiry: credentials are only used to *establish* a
connection, and all authorization after that is cluster-side.

**Timeout, cause unclear** — add `--serverSelectionTimeoutMS 15000`; mongosh's
2000 ms default truncates the real error. Check the tunnel independently with
`nc -zv localhost 50141`.

**`show dbs` lists `admin`/`config`/`local`** — you are on an admin (password)
connection, not the IAM one.

## Reference

AWS's own description of the mechanism is in
[Connecting with AWS IAM identity authentication](https://docs.aws.amazon.com/documentdb/latest/developerguide/iam-identity-auth.html).
