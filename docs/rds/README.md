# RDS / Aurora

Opens an SSM port-forward to an RDS or Aurora endpoint through the bastion host and
authenticates with an **IAM database auth token** instead of a stored password. den
mints the token with `aws rds generate-db-auth-token`, puts it where the client looks
for it, and re-mints it every 13 minutes while the tunnel is up (tokens live 15). A
database or user without IAM auth can log in with
[a password from a secret](#a-password-from-a-secret) instead.

Two engines are supported:

| `engine`             | Covers                                                  | Token goes to                               | Client  |
|----------------------|---------------------------------------------------------|---------------------------------------------|---------|
| `postgres` (default) | RDS PostgreSQL, Aurora PostgreSQL, RDS Proxy for either | `~/.pgpass` (`%APPDATA%\postgresql\pgpass.conf` on Windows, or `$PGPASSFILE` when set), with `update_pgpass: true` | `psql`  |
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
| `db_user`       | yes, unless `auth` is set | Database user with IAM auth enabled; the token is minted for it                            |
| `db_name`       | no              | The database to open. Left out, the client picks its own default (`psql` uses the user name)         |
| `update_pgpass` | no              | PostgreSQL only: keep `~/.pgpass` in sync with the current token. Not with `auth`                    |
| `auth`          | no              | Log in with a username and password from a secret: [below](#a-password-from-a-secret)                |
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
   the tunnel, checks the local port accepts connections, then mints the token and
   [logs in once with it](#the-login-check) to see that the database takes it.
2. Press `y` to copy the connect command.

   PostgreSQL. The password comes from `~/.pgpass`:
   ```bash
   psql "host=localhost hostaddr=127.0.0.1 port=50111 dbname=orders user=app sslmode=require"
   ```
   `hostaddr=127.0.0.1` because the tunnel listens on IPv4 only: with `localhost` alone
   `psql` tries `::1` first, and a failed login then also reports `Connection refused`,
   which reads as a dead tunnel. `host=localhost` stays, as the name `~/.pgpass` entries
   are matched on.

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

### The login check

RDS takes any token den mints and refuses it only when a client logs in, with no reason
given (`PAM authentication failed`). So after connecting, and after each new token (the
auto-refresh, `r`), den logs in once in the background, as a terminal tab would: the same
connection string, the token handed over the same way (`PGPASSWORD`, or the option file for
MySQL), and a query that returns at once:

```bash
psql -X -w -A -t -c "select 1" "host=localhost hostaddr=127.0.0.1 port=50111 dbname=orders user=app sslmode=require"
mysql --defaults-extra-file=~/.config/den/mysql/50131.cnf -h 127.0.0.1 -P 50131 -u app_iam \
  --enable-cleartext-plugin --ssl-mode=REQUIRED --batch -e "select 1" orders
```

The log says `login checked as app`. When the database refuses, the tunnel stays up, the
Connect panel shows `connected · login failed` and the detail view `CONNECTED · LOGIN FAILED`,
and the log has the database's answer and what den knows about the token:

```
login failed: FATAL:  PAM authentication failed for user "app"
the IAM token was minted as AWS profile "acme-int" (aws_profile: credential_profile is not set)
for user "app" on orders.cluster-x.eu-central-1.rds.amazonaws.com:5432; check that:
  - the user has the rds_iam role: GRANT rds_iam TO "app";
  - the profile may call rds-db:connect on
    arn:aws:rds-db:eu-central-1:<account>:dbuser:<resource-id>/app
  - the profile's credentials have not expired (aws sso login --profile acme-int)
```

Fix what it names and press `r`: den mints a token and checks again. With a password from a
secret the same check runs with the password, and the hint names the secret. Without the
client installed the log says `psql not found — login not checked`; nothing is marked failed.
`den list --json` and [`den mcp`](../mcp/README.md) report a refusal as `login_error`.

## A password from a secret

Not every database can use IAM auth: it may be switched off, or you may not be allowed
`rds-db:connect`. With `auth:`, the service logs in with a username and password kept in
one of your [secrets](../secrets/README.md) instead. The tunnel works as before, and
`den.yaml` still names the secret and never holds the value, so it stays safe to commit.

```yaml
secrets:
  aws_secretsmanager:
    - name: "Orders DB login"
      secret_id: "/acme/orders/dev/db-login"
      aws_profile: "acme-dev"

services:
  - name: "EU DEV Orders DB"
    type: rds
    env: eu-dev
    rds:
      local_port: 50111
      rds_host: "db1.abcdefghijkl.eu-central-1.rds.amazonaws.com"
      db_name: orders
      auth:
        secret: "Orders DB login"   # a name from secrets:
```

| Key                 | Required | Description                                                                 |
|---------------------|----------|-----------------------------------------------------------------------------|
| `auth.secret`       | yes      | The name of an entry under `secrets:`: `sops`, `aws_secretsmanager`, or one `vault` secret. Not a Vault folder, and not KeePassXC yet: den cannot ask for its master password while connecting |
| `auth.username_key` | no       | The key holding the username, dotted as in the flat view (`db.user`). Default `username` |
| `auth.password_key` | no       | The key holding the password, dotted as in the flat view (`db.password`). Default `password` |
| `db_user`           | no       | Wins over the secret's username, for a secret that holds only a password    |

The default keys are the ones AWS stores for a database, so a secret RDS manages for you
works as it is. Setting `update_pgpass` together with `auth` is a config error.

### Prerequisites

- **Tunnel:** the same as with a token: `aws_profile` opens the session. `credential_profile`
  and `rds-db:connect` are not used.
- **Secret:** read access to it: `secretsmanager:GetSecretValue`, the key of a SOPS file, or
  a Vault token. A permissions list for each is in [Secrets](../secrets/README.md#prerequisites).
- **Database:** a user with a password. On PostgreSQL it must not have been granted `rds_iam`,
  which makes RDS expect a token from that user. TLS stays required (`sslmode=require`,
  `--ssl-mode=REQUIRED`).

### Usage

`c` connects as usual, with two differences. den checks the secret's status first, so an
expired login shows up before a session is opened; the log says which secret and why. And the
token bar is gone: nothing counts down on a password, so the detail view says where it comes
from instead.

```
 psql "host=localhost hostaddr=127.0.0.1 port=50111 dbname=orders user=app sslmode=require"   y: copy
 ──────────────────────────────────────────────────────────────────────────────
 ─ Login
 Password from "Orders DB login" (aws secrets manager), read at 14:02  r: read again  p: copy
```

| Key | With a password                                                                    |
|-----|------------------------------------------------------------------------------------|
| `t` | The client in a terminal tab, already logged in                                    |
| `r` | Read the secret again. A connect and a reconnect read it too; nothing does it by itself |
| `p` | Copy the password; the clipboard is cleared after 30 s                             |
| `y` | Copy the connect command, which names the user and never holds the password        |
| `Y` | Not offered: the command would end up in your shell's history, still valid         |

**The password reaches no file.** den holds it in memory while connected and hands it to a
terminal tab through the client's environment (`PGPASSWORD`, `MYSQL_PWD`), so it needs no
escaping however awkward it is. If `~/.pgpass` still has a token from when the service used
IAM on that port, den removes that line: `psql` would send the dead token instead of asking.

So a client started outside den has to ask:

| Client                         | How it logs in                                                |
|--------------------------------|---------------------------------------------------------------|
| The `y` command in your terminal | `psql` or `mysql -p` asks for the password; `p` copies it   |
| A GUI client                   | Paste it once; saving it there is your choice                 |
| A runbook                      | It gets the port and no password, and has to read the secret itself |
| An AI agent (`den mcp`)        | It can open the tunnel and is not given the password          |

`MYSQL_PWD` is marked deprecated by MySQL as of 8.4. It works in the 9.3 client; if a future
client drops it, a tab fails with `Access denied` and `mysql -p` with `p` still works.

## Troubleshooting

- **`secret "…" (Secrets pane): …` in the log:** with `auth:`, the secret is not ready, and no
  session was opened. The rest of the line says why, as the Secrets pane does; log in there
  or in the AWS pane, and press `c` again.
- **`secret "…": no key "password" (it has: …)`:** set `auth.password_key` to one of the keys
  listed. A key inside a nested file is dotted: `db.password`.
- **`PAM authentication failed` with `auth:`:** the user has `rds_iam`, so RDS expects a token
  from it. Revoke the role, or log in with IAM by removing `auth:`.
- **`password authentication failed` with `auth:` after a rotation:** press `r` to read the
  secret again, and open a new tab; open sessions stay logged in.
- **`connected · login failed`:** the tunnel is up and the database refused den's own
  login with the credential it hands you; see [the login check](#the-login-check). The
  detail view's log has the database's answer and what to check.
- **`PAM authentication failed` (PostgreSQL) / `Access denied` (MySQL):** the
  token was minted fine but the database rejected it. Check the database-side grant
  above, that `db_user` matches the user in the `rds-db:connect` resource ARN, and the
  profile the log says the token was minted as: without `credential_profile` it is
  `aws_profile`, the role that opens the tunnel, which may not be allowed to log in.
- **`Authentication plugin 'mysql_clear_password' cannot be loaded`:** the
  `--enable-cleartext-plugin` flag was dropped, or the client is too old.
- **The token expired mid-session:** existing connections stay authenticated.
  Only new connections need a fresh token, which den writes before the old one expires.
  The log line of each token says when: `IAM token generated (expires 14:43:13,
  auto-refresh at 14:41:13)`.
