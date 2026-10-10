# DocumentDB

mongosh with your IAM identity, no password

## What it is

An Amazon DocumentDB cluster reached through a tunnel, logged in to with your AWS identity (the `MONGODB-AWS` mechanism) instead of a database password. Unlike the other stores there is no token: the driver signs its login with your AWS profile, and the cluster asks AWS who you are. So den has nothing to renew; it opens the tunnel, fetches the CA bundle and builds the `mongosh` command.

## What you can do

- Connect a cluster from the Connect panel and copy a `mongosh` command that works through the tunnel as written.
- Open `mongosh` in a terminal tab inside den, with `AWS_PROFILE` set to the identity the cluster knows.
- Have the Amazon RDS CA bundle downloaded for you the first time, so TLS is verified.
- Reopen the forward after an idle timeout with `reconnect`.
- Keys: press `h` in the detail view.

## Before you start

- `mongosh` 2.5 or newer (`den doctor --for documentdb`): older builds cannot log in with `MONGODB-AWS` at all.
- A working AWS login for `credential_profile`: it is the identity `mongosh` logs in as.
- That identity's role registered as a user in the cluster's `$external` database, with a role on your database. This is set up in the cluster, not in IAM.
- No IAM permission policy: the cluster only calls `sts:GetCallerIdentity`.

## Configure

A service of `type: documentdb` with a `documentdb:` section; the shared keys are in Services.

- `documentdb_host` (required): the cluster endpoint.
- `db_name`: the database the command opens; otherwise pick it in the shell with `use`.
- `ca_file` (default `~/.config/den/global-bundle.pem`, downloaded when missing): the CA bundle `mongosh` verifies the cluster with.
- `local_port` (default `27017`): where the cluster appears on `127.0.0.1`.

A cluster through the environment `dev`:

```yaml
services:
  - name: catalog dev
    type: documentdb
    env: dev
    documentdb:
      documentdb_host: catalog.cluster-abc123.eu-central-1.docdb.amazonaws.com
      local_port: 57017
```

Opening a database straight away, reopened after a drop, with a CA bundle you keep yourself:

```yaml
services:
  - name: catalog prod
    type: documentdb
    env: prod
    documentdb:
      documentdb_host: catalog.cluster-def456.eu-central-1.docdb.amazonaws.com
      local_port: 57018
      db_name: catalog
      ca_file: ~/certs/rds-global-bundle.pem
      reconnect: true
```

## Limitations

- One node through one tunnel: the command connects directly (`directConnection=true`), without replica-set discovery, whose in-VPC hostnames would not resolve on your machine.
- DocumentDB does not support retryable writes; the command turns them off.
- Everyone using the same SSO role logs in as the same database user: DocumentDB drops the session name, so its audit log cannot tell people apart.
- AI agents through `den mcp` get the command and the AWS profile, never more.

## When it goes wrong

- `MongoServerSelectionError: Server selection timed out`: not an authentication error. Replica-set discovery was turned on; keep `directConnection=true` and leave out `replicaSet`.
- Logins fail only sometimes: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` or `AWS_SESSION_TOKEN` exported in your shell win over `AWS_PROFILE`; unset them.
- Connected but `authenticatedUsers` is empty in `db.runCommand({connectionStatus: 1})`: the login did not happen; check the `$external` user.

## See also

- Services
- Terminal tabs
- `docs/documentdb/README.md`
