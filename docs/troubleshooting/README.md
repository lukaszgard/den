# Troubleshooting

When something fails, start with `den doctor`: it checks the tools, the AWS profiles, the
logins and the local ports den.yaml needs, and prints the command that fixes each gap.
Add `--deep` to also ask SSM whether each bastion is online. The Logs panel (`L`) and
the service's own log show what den ran and what it answered.

Each entry below says what you see, why it happens and what to do, and links to the page
that goes deeper. Errors specific to one data store are in the Troubleshooting section of
its page: [RDS](../rds/README.md#troubleshooting),
[ElastiCache](../elasticache/README.md#troubleshooting),
[DocumentDB](../documentdb/README.md#troubleshooting),
[Redshift](../redshift/README.md#troubleshooting),
[OpenSearch](../opensearch/README.md#troubleshooting),
[Neptune](../neptune/README.md#troubleshooting).

## AWS and the tunnel

### The SSO login has expired

**You see:** `ExpiredToken`, `Unable to locate credentials`, `SSO login expired`, a service
that fails at once with an AWS error, a secret that is not ready, or an update check that
fails right after start.

**Why:** the AWS SSO token behind a profile lasts a few hours. den cannot refresh it, and
every command it runs as that profile fails the same way.

**Fix:** log in again from den's AWS panel (`Enter` on the session), or with
`aws sso login --profile P`. Secrets and services become ready by themselves afterwards;
press `c` again for an update check. `den doctor` reports an expired session as a
warning. See [AWS SSO](../aws/README.md).

### The Session Manager plugin is missing

**You see:** the service log shows the AWS CLI's `SessionManagerPlugin is not found`, and
`den doctor` fails `session-manager-plugin`.

**Why:** an SSM port-forward is run by the AWS CLI, which hands the session to a separate
plugin. Without it the CLI starts and stops at once.

**Fix:** `den doctor` prints the install command for your OS and package manager; run it,
then run `den doctor` again. The commands for every platform are in
[Installing what den needs](../doctor/install.md).

### The local port is already in use

**You see:** `port 5555 is already in use — is another ssh/tunnel still running?` for a
tunnel, `port 5432 is already in use — is another SSM session or den still running?` for
a service (the transport's name in place of `SSM session`), a service that never reaches
`CONNECTED`, or `den doctor` warning `ports in use` or `local ports` (two services share
a port).

**Why:** a tunnel is ready when its local port accepts connections, so something else
holding the port, often a den you left running, an `aws ssm start-session` of your own
or an old `ssh -L`, either blocks the tunnel or answers in its place. Two services with
the same `local_port` cannot be connected at once.

**Fix:** find the holder with `lsof -i :PORT` (on Windows `netstat -ano`) and stop it, or
give the service another `local_port`. See [Configuration reference](../config/README.md).

### The forward drops after a while

**You see:** `Your session timed out due to inactivity and has been terminated.` in the
log, a tunnel that worked and now refuses connections, or an EC2 Instance Connect tunnel
that closes after a time.

**Why:** SSM closes an idle port-forward, and EC2 Instance Connect tunnels are
time-limited. Open connections end with it.

**Fix:** set `reconnect: true` on the service (or press `a` on it to toggle it): den
reopens the forward after a drop, and the credential is renewed while connected either
way. See [DocumentDB › Auto-reconnect](../documentdb/README.md#auto-reconnect) and
[Transports](../transports/README.md).

### `TargetNotConnected`

**You see:** the SSM session fails with `TargetNotConnected`.

**Why:** the bastion's SSM agent is offline, or `ec2_instance_id` names an instance in
another account or region than the profile and `aws_region_code`.

**Fix:** `den doctor --deep` asks SSM whether each bastion is online. Check the instance
ID, the profile and the region of the environment. See
[Environments](../environments/README.md#troubleshooting).

### `Permission denied (publickey)`

**You see:** the log of an `ssh` transport or an SSH tunnel ends in
`Permission denied (publickey)`.

**Why:** ssh found no usable key, and den cannot answer a password prompt.

**Fix:** load the key into your agent (`ssh-add`) or name it in the transport's `args`
(`-i ~/.ssh/key`). See [Transports](../transports/README.md) and [Tunnel](../tunnel/README.md).

## Credentials and clients

### The tunnel is up but the store refuses the login

**You see:** `PAM authentication failed` or `Access denied` (RDS), `WRONGPASS invalid
username-password pair` (ElastiCache, MemoryDB), `password authentication failed for user
"IAMR:…"` (Redshift), `AccessDenied` on `GetClusterCredentialsWithIAM`.

**Why:** the tunnel and the credential are fine and the store rejected it. The usual
causes are a missing IAM permission (`rds-db:connect`, `elasticache:Connect`,
`memorydb:Connect`, the Redshift credential actions), the database user not set up for IAM
authentication, or a token minted as a different profile than the one granted: the store
checks `credential_profile`, not `aws_profile`.

**Fix:** grant the `credential_profile` identity the permission on the right resource, and
check the user's side (`GRANT rds_iam TO …`, an `iam` authentication mode). The prerequisites
list on each store's page names the exact grants. For MemoryDB make sure `memorydb: true`
is set, since a token signed for the wrong service is rejected.

### The tunnel is up but no data comes back

**You see:** the client connects and then hangs or errors: `MongoServerSelectionError:
Server selection timed out`, `MOVED 10.x.x.x:6379`, a `mysql` client that ignores the tunnel,
a `kubectl` forward that gets no answer.

**Why:** one tunnel reaches one address. DocumentDB's driver discovers the replica set and
then tries the in-VPC hostnames, a cluster-mode cache answers keys on other shards with
`MOVED` to addresses only the VPC can reach, `localhost` makes the mysql client use a Unix
socket instead of the tunnel, and a `kubectl` resource that is not itself serving the
store's port has nothing to forward to.

**Fix:** use the connect command den copies (`y`) rather than writing your own: it carries
`directConnection=true`, `-h 127.0.0.1` and the right TLS options. Do not use `redis-cli -c`
through a tunnel. For `kubectl`, put a TCP proxy in front of the store inside the cluster.
See [DocumentDB](../documentdb/README.md#troubleshooting),
[ElastiCache](../elasticache/README.md#limitations) and [Transports](../transports/README.md).

### A client stops working after a while

**You see:** a client that reconnects by itself fails with a login error it did not have
before.

**Why:** an IAM token is checked when a connection opens, and lives 15 minutes. den
re-mints it every 13 minutes while the tunnel is up and writes it where the client looks,
but a client that reuses the old one on its own reconnect has an expired token.

**Fix:** open a new connection, or press `r` in the detail view to mint a token now and `y`
to copy the command again. Open sessions keep working.

### The terminal shows empty boxes instead of icons

**You see:** `􏿽` or empty boxes in the menu and the service list.

**Why:** den draws its icons with Nerd Font glyphs, and the terminal's font has none.

**Fix:** install a Nerd Font (`brew install --cask font-jetbrains-mono-nerd-font`) and set
the terminal's font to it. See [Quickstart](../quickstart/README.md#before-you-start).

## VPN

### openfortivpn is too old for SAML

**You see:** `WARN: Bad key in configuration file: "saml-login"`, then
`VPN account password:` and `ERROR: Could not authenticate to gateway`, or without the
helper `unrecognized option '--saml-login'`.

**Why:** openfortivpn older than 1.23.0 has no SAML login. Ubuntu 24.04 and older, Debian 12
and older and RHEL 9 ship one.

**Fix:** upgrade it. `den doctor --for vpn` prints the package command, or the steps to
build it from source where no package is new enough; then run `den vpn install-helper`
again. See [VPN › Troubleshooting](../vpn/README.md#troubleshooting).

### The kernel has no PPP (WSL)

**You see:** the SAML login works, then `Couldn't open the /dev/ppp device`, `Please load the
ppp_generic kernel module` and `The kernel does not support PPP`; den stops openfortivpn and
shows the cause. `den doctor` fails or warns `kernel PPP`.

**Why:** the VPN's pppd needs a PPP driver. The kernel of WSL 3.0.1 is built without one, and
on other Linux kernels the module may not be loaded, or its files were removed by an upgrade
since boot. Creating `/dev/ppp` by hand does not help.

**Fix:** on WSL 3.0.1 roll WSL back to 2.7.14, or use FortiClient on Windows. Elsewhere,
`sudo modprobe ppp_generic`, or reboot after a kernel upgrade. See
[VPN › Linux kernel and WSL](../vpn/README.md#linux-kernel-and-wsl).

### The gateway answers `Forbidden`

**You see:** the SAML page says `Forbidden … /remote/saml/start`.

**Why:** the gateway counts too many sessions for your account, often stray openfortivpn
processes left by earlier failed logins.

**Fix:** `sudo pkill -x openfortivpn` and `sudo pkill -x pppd` clear them, the live one
included; then connect again. See [VPN › Leaked sessions](../vpn/README.md#leaked-sessions).

## Install and updates

### The update fails its checksum

**You see:** `checksum mismatch for den_…: got …, want …` in the install steps of the
Updates panel, or from `install.sh` / `install.ps1`.

**Why:** the downloaded archive is not the one the release's `checksums.txt` describes: the
download was damaged, or it was altered.

**Fix:** nothing is installed, and the running den is untouched. Run the install again; if
it repeats, report it, and do not install the archive by hand. A release that publishes no
`checksums.txt` installs with a warning that the download was not verified. See
[Updates](../updates/README.md) and [Install](../install/README.md#troubleshooting).

### A new release broke something

**You see:** something that worked in the previous den fails after an update.

**Why:** a regression in the release, or a change it made on purpose (its notes, `s` in the
Updates panel, say which).

**Fix:** go back: `v` in the Updates panel, the release you ran before, `enter`, `y`; or
`den update install vX.Y.Z --skip-current` from a shell. The version you leave is skipped
on this machine, so the next check does not offer it again; `den update unskip` lets it back.
For a whole team, pin the good version with `updates.pin`. Report the regression: the
maintainers can yank the release for everyone. Before 1.8.0, den had no picker; use
`install.sh --version`. See [Updates › Choosing a version](../updates/README.md#choosing-a-version).

### den cannot write the update

**You see:** the install fails with a permission error.

**Why:** den replaces its binary with a rename next to it, so a den under a root-owned
directory cannot update itself.

**Fix:** use the commands the Updates panel prints, or reinstall into a folder you own
(`--dir`). See [Updates](../updates/README.md#prerequisites).

### `den` is not found, or is an older one

**You see:** `command not found` after the install, or `den --version` shows a version older
than the one you installed.

**Why:** the install folder is not on `PATH`, or another `den` earlier on `PATH` shadows the
new one. On Windows, the `PATH` change reaches only terminals opened afterwards.

**Fix:** add the folder the installer printed to your shell's startup file and open a new
terminal; remove the older copy the installer names. See
[Install](../install/README.md#troubleshooting).

### A tool is installed but den cannot use it

**You see:** `den doctor` reports a tool as off `PATH` or too old, or `aws` stays at v1 after
installing v2.

**Why:** Homebrew installs `libpq` and `mysql-client` keg-only, so they are on disk but not on
`PATH`. A package manager keeps the version a distribution shipped. A v1 AWS CLI from `pip`
sits in `~/.local/bin`, which Ubuntu puts first.

**Fix:** follow the steps `den doctor` prints for your machine (`brew link --force …`,
`python3 -m pip uninstall -y awscli`, a source build), then run `hash -r` and `den doctor`
again. See [den doctor](../doctor/README.md).

## Config, panels and agents

### den.yaml is not applied, or a key is ignored

**You see:** a panel that lacks the service you added, an error with a `file:line`, or a
warning in the Config panel naming a key.

**Why:** den loads the first file it finds (`-c`, `./den.yaml`, `~/.config/den/den.yaml`), which
may not be the one you edited. A file with errors is not applied when reloaded; an unknown key
is a warning and is ignored.

**Fix:** the Config panel shows the path den loaded and every problem. Fix the lines it lists,
or check the key against the [configuration reference](../config/README.md). With the JSON
Schema in your editor a misspelt key is underlined as you type
([den.yaml schema](../schema/README.md)). See [Editing den.yaml](../config-reload/README.md).

### `unknown env`

**You see:** `service[0] "x": unknown env "y"` or `runbook "x": unknown env "y"`.

**Why:** `env:` names an environment that is not under `environments:`. Names are
case-sensitive.

**Fix:** correct the name or add the environment. See [Environments](../environments/README.md).

### A runbook is missing or cannot reach the database

**You see:** a runbook that does not appear in the panel, or a script that cannot connect to
the database.

**Why:** the panel's warnings name the reason for a missing runbook: an unknown key, an unknown
`env`, a name that clashes, or an `env_map` that does not map the source's environment. A
script finds the tunnel through `DEN_PORT_*`, which exist only for services connected in the
running dashboard.

**Fix:** press `w` in the Runbooks panel to read the warnings; connect the service before
running the runbook. See [Runbooks](../runbooks/README.md) and
[Runbook sources](../runbook-sources/README.md).

### An agent cannot connect a production service

**You see:** a connect through `den mcp` is refused, or the agent sees no services.

**Why:** production services need `mcp.allow_production: true` and then your confirmation in the
MCP client for each connect, so a client that cannot ask you (no elicitation support) is
refused. `mcp.allow` patterns match whole names, and a pattern that matches none hides everything.

**Fix:** use a client with elicitation support, and check the patterns. See
[den mcp](../mcp/README.md) and [Security model](../security/README.md).
