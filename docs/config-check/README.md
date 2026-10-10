# Checking den.yaml

![config-check.gif](config-check.gif)

## What it is

den checks `den.yaml` whenever it reads the file, and `den config --check` runs the
same check on its own, for you or for a pipeline. It lists **every** problem, not the
first, each with:

- the line it is on;
- the YAML key (`services[2].redis.local_port`) and a stable **code**
  (`port-shared`), for scripts;
- a **hint** on how to fix it, under the message: `→ give each its own local_port`.

There are two kinds of problem:

- **Errors** keep den from using the file: YAML that does not parse, a value of the
  wrong kind, a missing or contradictory setting. A file with an error is never applied.
- **Warnings** are things den runs with, but you probably did not mean. The file is
  applied anyway, because a config that ran yesterday must still load, and a missing
  profile only breaks the services that use it.

The check reads the file and then looks outside it, at the files it points at and the
AWS files its profiles live in. It does not look at what only a running machine can tell
(tools on `PATH`, SSO logins, a port that is busy now): that is `den doctor`, which shows
the warnings below as well.

## Configuration

None. There is no key to turn the check on or off.

### What it checks

Errors come from den's own validation of the file, with the line of the entry:

| Code | Example | Fix |
|------|---------|-----|
| `yaml-syntax` | `not valid YAML: did not find expected key` | fix the line it names |
| `type` | `expected a number, got `abc`` | write a value of the right kind |
| `invalid` | `service[1] "x": unknown env "eu"` | define it under `environments:`, or correct the name |

Every `required` key and every `enum` of the [schema](../schema/README.md) is an error
when it is missing or wrong; a test holds the schema and the check to each other.

Warnings, in groups:

| Code | Example message | Fix |
|------|-----------------|-----|
| `unknown-key` | `unknown key "reconect", ignored` | the hint names the key you most likely meant |
| `unknown-panel` | `menu_order: unknown panel "runbok", ignored` | use a panel name den has |
| `log-level` | `logs.level: "verbose" is not a level` | trace, debug, info, warn or error |
| `path-missing` | `secrets.sops[0].file: /home/me/work/db.yaml does not exist` | create it, or correct the path |
| `path-not-dir`, `path-not-file`, `path-not-executable` | `secrets.vault[0].cli_path: /tmp/vault is not an executable file` | point it at the right kind of thing |
| `path-permissive` | `vpn[0].config_file: /home/me/.config/den/vpn/office.conf can be read by other users (mode 0644)` | `chmod 600` it: an openfortivpn config may hold a password |
| `path-in-repo` | `vpn[0].config_file: … is inside the git work tree /home/me/work/den` | keep it in `~/.config/den/vpn/`, or git-ignore it |
| `port-shared` | `local port 16379 is used by "A Redis" and "B Redis": only one can be connected at a time` | give each its own `local_port` |
| `port-range` | `"orders": local port 70000 is not a port (1-65535)` | use a port between 1024 and 65535 |
| `port-privileged` | `"orders": local port 80 is below 1024, binding it needs root` | use a port above 1024 |
| `profile-missing` | `AWS profile "devops" (used by orders) is not in ~/.aws/config` | the hint suggests a close name, or `aws configure sso --profile NAME` |
| `profile-bare` | `AWS profile "base" is written [base] in ~/.aws/config` | write it `[profile base]` |
| `profile-source-missing` | `AWS profile "role" takes its credentials from "base" (source_profile), which is not in ~/.aws/config` | define the source profile |
| `aws-config-missing` | `the AWS config ~/.aws/config does not exist, and den.yaml uses AWS profiles` | `aws configure sso`, or set `aws.config_path` |
| `sso-session-unknown` | `aws.sessions[0]: no [sso-session corp] in ~/.aws/config, skipped` | add the session, or correct the name |
| `name-duplicate` | `services[3] "orders": the name is already used by services[0], and only one of them is kept` | rename one |
| `region-format` | `environments.dev.aws_region_code: "Frankfurt" does not look like an AWS region code` | write `eu-central-1` |
| `instance-format` | `services[0].rds.ec2_instance_id: "i-123" does not look like an EC2 instance id` | copy the id from the EC2 console |

How each is decided:

- **Paths.** Each path is read the way den reads it, and the message shows the result,
  so a surprising base shows. `~` is expanded, except in the Docker Compose keys, where
  den passes the path on as written. Relative paths start from den.yaml's folder for
  `runbooks.dirs`, `runbooks.template`, `runbooks.items[].cwd` and `vpn[].config_file`
  (which falls back to the folder den runs in when the file is only there), and from the
  folder den runs in for everything else. Checked: `aws.config_path`,
  `aws.credentials_path`, `secrets.sops[].file`, `secrets.keepassxc[].database`,
  `key_file` and `cli_path`, `secrets.vault[].ca_cert` and `cli_path`,
  `services[].documentdb.ca_file`, `services[].docker.compose_file` and `env_file`,
  `vpn[].config_file`, and the runbook paths. The first `runbooks.dirs` folder may
  be missing, `den runbook new` makes it (no other is made, so a missing one is reported); a `logs.file_location` may name a
  folder that is not there, but not one that exists.
- **Ports.** Every forwarded service has a local port, its own default when it sets
  none (RDS 50432, Redshift 55439, Redis 16379, DocumentDB 27017, OpenSearch 59200,
  Neptune 58182, Kafka 19092), so two Redis services that set none share one. The ports
  in a tunnel's command count too: every `-L [bind:]port:host:hostport` and
  `-D [bind:]port`.
- **AWS profiles.** The profiles a service runs as (through its environment), a secret
  uses, or `updates` uses, are looked up in the AWS config and credentials files, and a
  `source_profile` chain is followed. The profiles of an environment no service uses yet
  are looked up too, as are its region code and instance id. They are not checked for a
  login.
- **Names.** Two entries of one kind with the same type and name are one to den, and
  the second never appears. An RDS and a Redis service may share a name.

## Prerequisites

None. The check only reads files: it never changes anything and calls no AWS API.

## Usage

### On the command line

```
$ den config --check
den.yaml: 1 error, 3 warnings
✗ den.yaml:12  service[1] "x": unknown env "eu"
    → define it under environments:, or correct the name
! den.yaml:40  local port 50432 is used by "A RDS" and "B RDS": only one can be connected at a time
    → give each its own local_port
! den.yaml:44  unknown key "reconect", ignored
    → did you mean "reconnect"?
```

`den config --check FILE` checks that file instead of the one den finds (`-c`, then
`./den.yaml`, then `~/.config/den/den.yaml`); it must exist. With nothing to report it
prints `den.yaml: no problems`.

| Flag | What it does |
|------|--------------|
| `--check` | Print the problems instead of the config |
| `--json` | Print `{path, errors, warnings, problems: [{severity, line, key, code, message, hint}]}` |
| `--strict` | Exit 1 on warnings too |

The exit code is 1 when there is an error (and with `--strict` a warning), 0 otherwise,
so a pipeline can gate on it:

```yaml
# .github/workflows/den.yaml
- run: den config --check --strict den.yaml
```

```
den config --check --json | jq -r '.problems[] | select(.code == "port-shared") | .message'
```

Plain `den config` uses the same check: warnings go to the error stream, and a file with
errors fails.

### In the dashboard

The Config panel shows the same problems.

- Above the file: the first five, each with its hint under it, and
  `… and 7 more — p lists them all`. Their lines are marked beside the file.
- The menu entry counts them (`Config ● 3`), and starting den with warnings only flashes
  that once in the status bar.

| Key | What it does |
|-----|--------------|
| `p` | Replace the file with the list of every problem, wrapped, with its hint |
| `↑` `↓` `pgup` `pgdn` | In the list: move between problems; the list scrolls to the cursor |
| `enter` | In the list: back to the file with that line at the top, marked `▶` |
| `esc` or `p` | In the list: back to the file |
| `n` / `N` | In the file: the next or previous marked line (round the end) |
| `e`, `r`, `h` | Edit, reload, shortcuts, as always |

See [Editing den.yaml](../config-reload/README.md) for what an edit does with errors.
