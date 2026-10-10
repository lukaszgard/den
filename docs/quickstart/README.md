# Quickstart

From a fresh machine to a `psql` prompt on a private RDS database, with den opening the
tunnel and minting the IAM credential. Most of the work is on the AWS side, which only
has to be done once.

## Before you start

- **AWS SSO access.** A profile in `~/.aws/config` that can see your data stores. Below,
  `P` stands for the profile you will point den at.
  [Configuring IAM Identity Center authentication](https://docs.aws.amazon.com/cli/latest/userguide/sso-configure-profile-token.html)
  (`aws configure sso`) describes how to set one up.
- **A bastion.** An EC2 instance in the database's network with the SSM agent online, and a
  profile allowed to start a port-forwarding session on it. `den init` finds it for you.
- **The AWS CLI v2 and its Session Manager plugin.** `den doctor` (step 3) checks both
  and prints the install command for your OS.
- **IAM database authentication** on the database, and your database user set up for it
  (for PostgreSQL, `GRANT rds_iam TO "your_user";`), with the `rds-db:connect`
  permission. [The RDS page](../rds/README.md#prerequisites) has the details.
- **A terminal with a Nerd Font.** den draws its icons with
  [Nerd Font](https://www.nerdfonts.com/) glyphs; without one they show as empty boxes.
  On macOS: `brew install --cask font-jetbrains-mono-nerd-font`, then set the terminal's
  font to `JetBrainsMono Nerd Font Mono`.
- **`psql`** for the last step (or the client of whichever store you connect).

## Steps

### 1. Install den

**macOS and Linux:**

```bash
curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://github.com/lukaszgard/den/releases/latest/download/install.ps1 | iex
```

The script checks the download against the release's checksums and puts `den` in
`~/.local/bin` (on Windows `%LOCALAPPDATA%\Programs\den`, added to your `PATH`). Open a new
terminal and check:

```bash
den --version
```

[Install](../install/README.md) has the options, a specific version and installing by hand.

### 2. Log in to AWS

```bash
aws sso login --profile P
```

### 3. Check the machine

```bash
den doctor
```

den doctor lists what is installed and what is not, and prints the command that fixes
each gap for your OS: the AWS CLI, the Session Manager plugin, `psql`. It only prints; run
the commands you want, then run it again until nothing fails. Without a `den.yaml` yet it
warns about that and says where to put one, which is fine (`den paths` lists every place den looks;
see [Files and folders](../files/README.md)). See [den doctor](../doctor/README.md).

### 4. Write a first `den.yaml`

```bash
den init --profile P --region eu-central-1 -o ~/.config/den/den.yaml
```

den init reads what the profile can see in that region (the SSM-managed instances and the
data stores behind them) and writes one environment and one service per store. With
several online bastions it asks which one to use. It only calls `Describe` and `List`
APIs, so nothing changes in your account.

Open the file and fill in what den cannot know: the `db_user` of your RDS service, which
is written as a comment. See [den init](../init/README.md).

### 5. Check the config

```bash
den doctor
```

Now it checks what the file needs: the AWS profiles it names, the SSO login, the local ports.

### 6. Start den and connect

```bash
den
```

The Connect panel is the first one. Press `→` to move into the service list, `/` to filter
it, then `c` on your RDS service. den opens the SSM tunnel, waits until the local port
accepts connections and only then mints the IAM token. The log below shows each step. The
service turns green when it is ready.

### 7. Open the database

In the service's detail view (`Enter`), press `t`. den runs `psql` in a
[terminal tab](../terminal/README.md) next to the logs, already logged in:

```text
orders=> select count(*) from orders;
```

`Esc` gives the keyboard back to den. To use your own terminal instead, press `y` for the
connect command, which never contains the credential:

```bash
psql "host=localhost port=50111 dbname=orders user=app_iam sslmode=require"
```

`d` disconnects, `h` lists the keys of the panel you are in, and `q` quits and stops every
tunnel.

## Next steps

- [Environments](../environments/README.md): write a bastion once and point services at it.
- [Configuration reference](../config/README.md) and [command-line reference](../cli/README.md).
- Another store: [ElastiCache](../elasticache/README.md), [DocumentDB](../documentdb/README.md),
  [Redshift](../redshift/README.md), [OpenSearch](../opensearch/README.md),
  [Neptune](../neptune/README.md).
- [Secrets](../secrets/README.md), [runbooks](../runbooks/README.md) and
  [AI agents](../mcp/README.md).
- [Shortcuts](../shortcuts/README.md): the keys of every panel.
- Something went wrong: [Troubleshooting](../troubleshooting/README.md), and the
  [glossary](../glossary/README.md) for the words den uses.
