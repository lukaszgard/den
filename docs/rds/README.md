# RDS / Aurora

Opens an SSM port-forward to an RDS or Aurora endpoint through the bastion host and
authenticates with an **IAM database auth token** instead of a stored password. den
mints the token with `aws rds generate-db-auth-token`, puts it where the client looks
for it, and re-mints it every 13 minutes while the tunnel is up (tokens live 15).

Two engines are supported:

| `engine`             | Covers                                                  | Token goes to                               | Client  |
|----------------------|---------------------------------------------------------|---------------------------------------------|---------|
| `postgres` (default) | RDS PostgreSQL, Aurora PostgreSQL, RDS Proxy for either | `~/.pgpass` (with `update_pgpass: true`)    | `psql`  |
| `mysql`              | RDS MySQL, RDS MariaDB, Aurora MySQL                    | `~/.config/den/mysql/<local_port>.cnf`      | `mysql` |

Any endpoint works as `rds_host`: instance, cluster (writer or reader) or RDS Proxy.

## Configuration

```yaml
services:
  # PostgreSQL — the default engine
  - name: "EU DEV Orders DB"
    type: rds
    env: eu-dev
    rds:
      local_port: 50111
      update_pgpass: true
      reconnect: true
      rds_host: "db1.abcdefghijkl.eu-central-1.rds.amazonaws.com"
      db_user: app              # required
      db_name: orders           # optional

  # MySQL / MariaDB / Aurora MySQL
  - name: "EU DEV MySQL"
    type: rds
    env: eu-dev
    rds:
      engine: mysql
      local_port: 50131
      rds_host: "orders.cluster-abc123.eu-central-1.rds.amazonaws.com"
      db_user: app_iam          # required
      db_name: orders           # optional
```

| Field           | Required        | Description                                                                                          |
|-----------------|-----------------|------------------------------------------------------------------------------------------------------|
| `rds_host`      | yes             | Endpoint to forward to: an instance, a cluster endpoint or an RDS Proxy                              |
| `engine`        | no              | `postgres` (default) or `mysql`. Picks the remote port (5432 / 3306), the client and where the token goes |
| `local_port`    | no              | Local end of the tunnel (default `50432`)                                                            |
| `db_user`       | yes             | Database user with IAM auth enabled; the token is minted for it                                      |
| `db_name`       | no              | The database to open. Left out, the client picks its own default (`psql` uses the user name)         |
| `update_pgpass` | no              | PostgreSQL only: keep `~/.pgpass` in sync with the current token                                     |
| `reconnect`     | no              | Auto-reopen the SSM port-forward when it drops (e.g. SSM idle timeout). The token is renewed while connected either way. See [DocumentDB › Auto-reconnect](../documentdb/README.md#auto-reconnect) |

The `env:` reference supplies `ec2_instance_id` (the bastion), `aws_profile` (opens
the SSM session), `credential_profile` (**the identity the token is minted as**) and
`aws_region_code`.

## Prerequisites

- **AWS CLI v2** and the **Session Manager plugin**.
- **A valid SSO session** for both profiles.
- **IAM:** `rds-db:connect` on
  `arn:aws:rds-db:<region>:<account>:dbuser:<DbiResourceId or cluster-resource-id>/<db_user>`
  for the `credential_profile` identity.
- **Database side:** IAM auth enabled on the instance/cluster, and the user set up for it:
  - PostgreSQL: `GRANT rds_iam TO "app";`
  - MySQL/MariaDB: `CREATE USER app_iam IDENTIFIED WITH AWSAuthenticationPlugin AS 'RDS';`
- **Client:** `psql`, or for MySQL the **MySQL 8+ client** (`brew install mysql-client`).
  The MariaDB client spells TLS differently (`--ssl` instead of `--ssl-mode=REQUIRED`).

## Usage

1. Launch `den`, open **Connect**, select the RDS service and press `c`. den opens
   the tunnel, checks the local port accepts connections, then mints the token.
2. Press `y` to copy the connect command.

   PostgreSQL. The password comes from `~/.pgpass`:
   ```bash
   psql "host=localhost port=50111 dbname=orders user=app sslmode=require"
   ```
   MySQL. The password comes from den's option file:
   ```bash
   mysql --defaults-extra-file=~/.config/den/mysql/50131.cnf -h 127.0.0.1 -P 50131 -u app_iam \
     --enable-cleartext-plugin --ssl-mode=REQUIRED orders
   ```
3. Or stay in den: in the detail view, `t` runs that client in a
   [terminal tab](../terminal/README.md) next to the logs, already logged in. `Esc`
   gives the keyboard back to den.
4. In the detail view, `Y` copies the same command with the token inline, for use on
   another machine. `p` copies only the token, and `r` re-mints it now.
5. `d` disconnects. It also stops the clients in the service's terminal tabs, so in
   the detail view it asks first while any of them is running.

### Why the MySQL command looks like that

| Part                          | Why                                                                                |
|-------------------------------|------------------------------------------------------------------------------------|
| `--defaults-extra-file` first | mysql rejects the option anywhere but first                                        |
| `-h 127.0.0.1`                | `localhost` makes the mysql client use the Unix socket, bypassing the tunnel        |
| `--enable-cleartext-plugin`   | IAM auth sends the token through the cleartext plugin; the client refuses otherwise |
| `--ssl-mode=REQUIRED`         | IAM auth requires TLS. `REQUIRED` encrypts without checking the hostname, which is `127.0.0.1` over the tunnel |

The token is signed for the RDS endpoint's host **and port** (5432 or 3306), not
the local end of the tunnel. That is why `engine` has to be right even though the
tunnel itself would forward any port.

## Troubleshooting

- **`PAM authentication failed` (PostgreSQL) / `Access denied` (MySQL):** the
  token was minted fine but the database rejected it. Check the database-side grant
  above, and that `db_user` matches the user in the `rds-db:connect` resource ARN.
- **`Authentication plugin 'mysql_clear_password' cannot be loaded`:** the
  `--enable-cleartext-plugin` flag was dropped, or the client is too old.
- **The token expired mid-session:** existing connections stay authenticated.
  Only new connections need a fresh token, which den writes before the old one expires.
  The log line of each token says when: `IAM token generated (expires 14:43:13,
  auto-refresh at 14:41:13)`.
