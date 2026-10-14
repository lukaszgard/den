# Redshift

Opens an SSM port-forward to an **Amazon Redshift** cluster or **Redshift Serverless**
workgroup through the bastion host, and logs in with **temporary database credentials**
minted from your IAM identity instead of a stored password.

Redshift speaks the PostgreSQL wire protocol, so the result looks like the `rds` service
type: den mints the credentials, writes them to `~/.pgpass`, and `y` copies a `psql`
command. The differences:

- The **user name comes from Redshift**, not from your config. It is `IAMR:<role>` or
  `IAM:<user>`, and logging in under any other name fails. den shows whatever Redshift
  returned, and escapes its colon in `~/.pgpass`.
- Credentials last **15 minutes to an hour** (`credential_ttl`). den re-mints them two
  minutes before they expire, and the TTL bar reflects the real lifetime.

Which API mints them depends on the config:

| Config                                | API                                     | Logs in as                           |
|---------------------------------------|-----------------------------------------|--------------------------------------|
| `workgroup_name`                      | `redshift-serverless:GetCredentials`    | your IAM identity (`IAMR:<role>`)    |
| `cluster_identifier`                  | `redshift:GetClusterCredentialsWithIAM` | your IAM identity (`IAMR:<role>`)    |
| `cluster_identifier` + `db_user`      | `redshift:GetClusterCredentials`        | that existing user (`IAM:<db_user>`) |

`GetClusterCredentials` is called with `AutoCreate=false`, so den never creates a
database user as a side effect.

## Configuration

```yaml
services:
  # Provisioned cluster
  - name: "EU DEV Analytics"
    type: redshift
    env: eu-dev
    redshift:
      local_port: 50151
      update_pgpass: true
      reconnect: true
      redshift_host: analytics.abc123xyz.eu-central-1.redshift.amazonaws.com
      cluster_identifier: analytics
      db_name: dev
      # db_user: analyst        # optional: log in as this existing user instead
      # credential_ttl: 1h      # 15m (default) … 1h

  # Serverless workgroup
  - name: "EU DEV Redshift Serverless"
    type: redshift
    env: eu-dev
    redshift:
      local_port: 50152
      update_pgpass: true
      redshift_host: analytics.123456789012.eu-central-1.redshift-serverless.amazonaws.com
      workgroup_name: analytics
```

| Field                | Required    | Description                                                                          |
|----------------------|-------------|--------------------------------------------------------------------------------------|
| `redshift_host`      | yes         | Cluster or workgroup endpoint, without the port                                      |
| `cluster_identifier` | one of      | Provisioned cluster identifier                                                       |
| `workgroup_name`     | one of      | Serverless workgroup name                                                            |
| `db_name`            | no          | Database to log in to (default `dev`, the database every cluster is created with)    |
| `db_user`            | no          | Provisioned only. Log in as this existing database user instead of your IAM identity |
| `credential_ttl`     | no          | Credential lifetime, `15m`–`1h` (default `15m`)                                      |
| `update_pgpass`      | no          | Keep `~/.pgpass` (`%APPDATA%\postgresql\pgpass.conf` on Windows, or `$PGPASSFILE` when set) in sync with the current credentials |
| `local_port`         | no          | Local end of the tunnel (default `55439`)                                            |
| `reconnect`          | no          | Auto-reopen the SSM port-forward when it drops (e.g. SSM idle timeout). Credentials are renewed while connected either way |

The `env:` reference supplies the bastion, `aws_profile` (opens the SSM session),
`credential_profile` (**the identity the credentials are minted as**) and `aws_region_code`.

## Prerequisites

- **AWS CLI v2** and the **Session Manager plugin**; a valid SSO session.
- **Network:** the bastion must reach the endpoint on port 5439.
- **IAM** for the `credential_profile` identity, one of:
  - Serverless: `redshift-serverless:GetCredentials` on the workgroup.
  - Provisioned, IAM identity: `redshift:GetClusterCredentialsWithIAM` on
    `arn:aws:redshift:<region>:<account>:dbname:<cluster>/<db_name>`.
  - Provisioned, `db_user`: `redshift:GetClusterCredentials` on
    `…:dbuser:<cluster>/<db_user>` and `…:dbname:<cluster>/<db_name>`.
- **Database side:** the first IAM login creates the `IAMR:`/`IAM:` user with no grants.
  Grant it what it needs, e.g. `GRANT USAGE ON SCHEMA sales TO "IAMR:dev-role";`.
- **`psql`**.

## Usage

1. Launch `den`, open **Connect**, select the Redshift service and press `c`. den opens
   the tunnel, checks the local port, then mints the credentials.
2. Press `y` to copy the command. The password comes from `~/.pgpass`:
   ```bash
   psql "host=localhost port=50151 dbname=dev user=IAMR:dev-role sslmode=require"
   ```
3. In the detail view, `Y` copies the command with the password inline, `p` copies only
   the password, and `r` re-mints now.
4. Or skip the copying: in the detail view, `t` runs `psql` in a
   [terminal tab](../terminal/README.md) next to the logs, already logged in. The
   password reaches it through `PGPASSWORD`, so this works with `update_pgpass` off.
5. `d` disconnects. It also stops the clients in the service's terminal tabs.

Before the first connect the command shows `<iam-user>` (or `IAM:<db_user>`), because
Redshift picks the name only when it mints the credentials.

## Troubleshooting

- **`password authentication failed for user "IAMR:…"`:** the credentials expired, or
  `~/.pgpass` is not in use (`update_pgpass` off, or the file is group-readable). Press
  `r`, or use `Y` to pass the password inline.
- **`permission denied for schema …` right after logging in:** authentication worked, but
  the auto-created IAM user has no grants yet (see Prerequisites).
- **`AccessDenied … GetClusterCredentialsWithIAM`:** the IAM policy grants the older
  `GetClusterCredentials` action only. Either add the `WithIAM` action or set `db_user`.
