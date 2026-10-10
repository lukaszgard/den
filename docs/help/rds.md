# RDS and Aurora

PostgreSQL and MySQL with IAM tokens, no passwords

## What it is

An RDS or Aurora database in a private subnet, reached through a tunnel and logged in to with an IAM auth token instead of a stored password. den opens the tunnel, mints the token as your AWS profile, puts it where `psql` or `mysql` looks for it, and mints a new one before the old one runs out. Use it whenever you would otherwise ask for a database password or open the port-forward by hand.

## What you can do

- Connect a database from the Connect panel: den opens the tunnel, waits until the local port answers, then mints the token.
- Work in `psql` or `mysql` without typing a password: for PostgreSQL the token goes to `~/.pgpass` (with `update_pgpass`), for MySQL to an option file den writes for the `mysql` client.
- Copy the connect command for your own terminal, the same command with the token inlined for another machine, or the token alone, from the detail view.
- Open the client in a terminal tab inside den, already logged in, from the detail view.
- Mint a new token on demand; otherwise den renews it every 13 minutes while connected (a token lives 15).
- Reach any endpoint: an instance, a cluster writer or reader, or an RDS Proxy.
- Keys: press `h` in the detail view.

## Before you start

- The client: `psql` for PostgreSQL, the MySQL 8 client for MySQL. `den doctor --for rds` (or `--for mysql`) checks it and the transport's tools.
- A working AWS login for the profiles of the service's environment.
- IAM: the `credential_profile` identity needs `rds-db:connect` on the user's resource, `dbuser:<resource-id>/<db_user>`.
- IAM auth turned on for the instance or cluster, and the user set up for it: `GRANT rds_iam TO "<db_user>";` on PostgreSQL, `CREATE USER <db_user> IDENTIFIED WITH AWSAuthenticationPlugin AS 'RDS';` on MySQL.

## Configure

A service of `type: rds` with an `rds:` section. The keys every data store shares (`env`, `local_port`, `reconnect` and the environment overrides) are in Services.

- `rds_host` (required): the endpoint to forward to.
- `engine` (default `postgres`): `postgres` for RDS and Aurora PostgreSQL, `mysql` for RDS MySQL, MariaDB and Aurora MySQL. It picks the remote port (5432 or 3306), the client and where the token goes.
- `db_user` (required for `mysql`): the database user with IAM auth.
- `db_name`: the database the client opens; MySQL leaves it to the client when unset.
- `update_pgpass` (default `false`, PostgreSQL only): keep `~/.pgpass` in sync with the current token.
- `local_port` (default `50432`): where the database appears on `127.0.0.1`.

PostgreSQL through the environment `dev`, with `psql` reading the token from `~/.pgpass`:

```yaml
services:
  - name: orders dev
    type: rds
    env: dev
    rds:
      rds_host: orders.cluster-abc123.eu-central-1.rds.amazonaws.com
      db_name: orders
      db_user: app_iam
      update_pgpass: true
```

The same cluster's reader endpoint next to it, on its own local port so both can be connected at once, and reopened by itself when the tunnel drops:

```yaml
services:
  - name: orders dev (reader)
    type: rds
    env: dev
    rds:
      rds_host: orders.cluster-ro-abc123.eu-central-1.rds.amazonaws.com
      local_port: 50433
      db_name: orders
      db_user: app_readonly
      update_pgpass: true
      reconnect: true
```

MySQL, MariaDB or Aurora MySQL; `db_user` is required here:

```yaml
services:
  - name: billing dev
    type: rds
    env: dev
    rds:
      engine: mysql
      rds_host: billing.cluster-abc123.eu-central-1.rds.amazonaws.com
      local_port: 53306
      db_user: app_iam
      db_name: billing
```

An RDS Proxy in production, reached through the environment `prod`:

```yaml
services:
  - name: orders prod
    type: rds
    env: prod
    rds:
      rds_host: orders-proxy.proxy-abc123.eu-central-1.rds.amazonaws.com
      local_port: 50434
      db_name: orders
      db_user: app_readonly
```

Without an environment: the bastion and the profiles are set on the service itself.

```yaml
services:
  - name: legacy reports
    type: rds
    rds:
      region: eu
      environment: legacy
      ec2_instance_id: i-0fedcba9876543210
      aws_profile: legacy-bastion
      credential_profile: legacy-data
      aws_region_code: eu-west-1
      rds_host: reports.abc123.eu-west-1.rds.amazonaws.com
      local_port: 50435
      db_name: reports
      db_user: reports_iam
```

## Limitations

- IAM authentication only: den cannot log in with a stored password or with Secrets Manager rotation.
- Two engines: PostgreSQL and MySQL-compatible. SQL Server, Oracle and Db2 are not supported.
- `engine` must match the database even though the tunnel would forward any port: the token is signed for the endpoint's own host and port.
- The last token stays in `~/.pgpass` after you disconnect; it is useless 15 minutes after it was minted.

## When it goes wrong

- `PAM authentication failed` or `Access denied`: the database rejected a valid token. Check the grant above, and that `db_user` is the user named in the `rds-db:connect` resource.
- `Authentication plugin 'mysql_clear_password' cannot be loaded`: the MySQL client is too old, or `--enable-cleartext-plugin` was dropped from the command.
- The token expired mid-session: open connections stay logged in; only new ones need a fresh token, which den writes before the old one expires. The detail view's log says when each token expires.

## See also

- Services
- Environments
- Terminal tabs
- `docs/rds/README.md`
