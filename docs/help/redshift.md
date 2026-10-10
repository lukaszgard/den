# Redshift

Provisioned and Serverless, temporary passwords

## What it is

An Amazon Redshift cluster or Redshift Serverless workgroup reached through a tunnel, logged in to with temporary database credentials minted from your IAM identity. Redshift speaks the PostgreSQL protocol, so it works like RDS: den mints the credentials, can keep `~/.pgpass` current, and gives you a `psql` command.

## What you can do

- Connect a cluster or workgroup from the Connect panel and use `psql` on `localhost` without a password.
- Log in as your own IAM identity (the user Redshift names `IAMR:<role>`), or as an existing database user on a provisioned cluster.
- Choose how long the credentials live, 15 minutes to an hour; den renews them two minutes before they expire.
- Copy the `psql` command, the same command with the password, or the password alone, from the detail view.
- Open `psql` in a terminal tab inside den, already logged in, even with `update_pgpass` off.
- Keys: press `h` in the detail view.

## Before you start

- `psql` (`den doctor --for redshift`).
- IAM for the `credential_profile` identity: `redshift-serverless:GetCredentials` on a workgroup; `redshift:GetClusterCredentialsWithIAM` on a cluster's database; or with `db_user`, `redshift:GetClusterCredentials` on that user and database.
- Grants in the database: the first IAM login creates its user with no rights, for example `GRANT USAGE ON SCHEMA sales TO "IAMR:dev-role";`.
- The bastion must reach the endpoint on port 5439.

## Configure

A service of `type: redshift` with a `redshift:` section; set exactly one of `cluster_identifier` and `workgroup_name`. The shared keys are in Services.

- `redshift_host` (required): the cluster or workgroup endpoint, without the port.
- `cluster_identifier`: a provisioned cluster's identifier.
- `workgroup_name`: a Serverless workgroup's name.
- `db_name` (default `dev`): the database to log in to.
- `db_user` (provisioned only): log in as this existing user instead of your IAM identity.
- `credential_ttl` (default `15m`): how long the credentials live, `15m` to `1h`.
- `update_pgpass` (default `false`): keep `~/.pgpass` in sync with the current credentials.
- `local_port` (default `55439`): where it appears on `127.0.0.1`.

A provisioned cluster, logged in to as your IAM identity:

```yaml
services:
  - name: analytics dev
    type: redshift
    env: dev
    redshift:
      redshift_host: analytics.abc123.eu-central-1.redshift.amazonaws.com
      cluster_identifier: analytics
      update_pgpass: true
```

A Serverless workgroup, with credentials that last an hour:

```yaml
services:
  - name: analytics dev (serverless)
    type: redshift
    env: dev
    redshift:
      redshift_host: analytics.123456789012.eu-central-1.redshift-serverless.amazonaws.com
      workgroup_name: analytics
      credential_ttl: 1h
      local_port: 55440
      update_pgpass: true
```

A provisioned cluster, logged in to as an existing user and another database:

```yaml
services:
  - name: finance reports prod
    type: redshift
    env: prod
    redshift:
      redshift_host: finance.def456.eu-central-1.redshift.amazonaws.com
      cluster_identifier: finance
      db_user: report_reader
      db_name: reports
      local_port: 55441
      reconnect: true
```

## Limitations

- The user name comes from Redshift, not from your config: until the first connect the command shows `<iam-user>`.
- `db_user` works on provisioned clusters only; Serverless always logs in as your IAM identity.
- den never creates a database user for `db_user`: it must already exist.
- Credentials live at most an hour; an open session keeps working, a new connection needs the current ones.

## When it goes wrong

- `password authentication failed for user "IAMR:…"`: the credentials expired, or `~/.pgpass` is not in use; mint new ones, or copy the command with the password inlined.
- `permission denied for schema …` right after logging in: the login worked; the IAM user has no grants yet.
- `AccessDenied … GetClusterCredentialsWithIAM`: the policy allows only the older `GetClusterCredentials`; add the `WithIAM` action, or set `db_user`.

## See also

- RDS and Aurora
- Services
- Terminal tabs
- `docs/redshift/README.md`
