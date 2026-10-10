# Getting started

Find, write, check and reload den.yaml

## What it is

Everything den shows comes from one file, `den.yaml`: the environments your data stores are reached through, the stores themselves, your tunnels, VPNs, secrets and runbooks. This topic is about the file itself: where den looks for it, how to get a first one written for you, and how to check it. Every other topic shows the part of it that feature uses.

## What you can do

- Install den itself with one command on a new machine: `curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh` (`install.ps1` on Windows).
- Have den write a first `den.yaml` from what one AWS profile can see: the SSM bastions and the data stores behind them, one service each.
- Get completion, descriptions and validation in your editor from den's JSON Schema, so a misspelt key is underlined before den loads it.
- Check that every tool, AWS profile, login and port the file needs is in place, with the install command for your OS for anything missing.
- See which file den loaded, highlighted, in the Config panel, and open it in your editor from there: it is applied when the editor closes.
- Reload the file without restarting: connections whose entry did not change stay up, and a mistake never stops den.
- Keys: press `h` in a panel to see what its keys do; this Help never lists them.

## Before you start

- For `den init`: the AWS CLI v2 and a working login for the profile you point it at, with read access to SSM, RDS, ElastiCache, MemoryDB, Redshift and OpenSearch. A missing permission skips that kind of store.
- For the schema: an editor with the YAML language server (the Red Hat YAML extension in VS Code, built into JetBrains IDEs, `yaml-language-server` in Neovim).
- Nothing else: `den doctor` tells you what each part of the file needs.

## Configure

den reads the first of these that exists: the file named by `-c` or `--config` (which must exist), `den.yaml` in the current directory, then `den.yaml` in the config directory (`~/.config/den`, or `$XDG_CONFIG_HOME/den`, or `%APPDATA%\den` on Windows). Your own files belong in the config directory. `den paths` shows which one den loaded and where everything else is.

- `version`: the config format version, `"1"`.
- Every other top-level section is optional and has its own topic: `environments`, `services`, `tunnels`, `vpn`, `secrets`, `runbooks`, `aws`, `mcp`, `ai`, `logs`, `updates`, `menu_order`, `disabled_panes`.

Write a starter file, check it, then start den with it:

```
den init --profile dev -o ~/.config/den/den.yaml
den doctor
den
```

Turn on editor completion: save the schema next to the file and name it on the file's first line.

```
den schema > ~/.config/den/den.schema.json
```

The smallest useful `den.yaml`: one environment and one database reached through it.

```yaml
# yaml-language-server: $schema=den.schema.json
version: "1"
environments:
  sandbox:
    region: eu
    environment: sandbox
    ec2_instance_id: i-0a1b2c3d4e5f60718
    aws_profile: sandbox
    credential_profile: sandbox
    aws_region_code: eu-central-1
services:
  - name: app db sandbox
    type: rds
    env: sandbox
    rds:
      rds_host: app.cluster-xyz789.eu-central-1.rds.amazonaws.com
      db_name: app
      db_user: app_iam
```

## Limitations

- den does not discover anything while it runs: `den init` writes a static file once, and a new database needs a new entry.
- `den init` cannot know your database user, cache user or which stores you actually use; it writes those as comments to fill in.
- A misspelt key does not stop den: it is ignored, and named as a warning in the Config panel. The schema in your editor underlines it as you type.
- A reload stops the connections whose entry changed, after asking; connect them again afterwards.

## When it goes wrong

- `config file …: no such file or directory`: `-c` names a file that does not exist. Without `-c` and with no file found, den starts with an empty dashboard and the Config panel says so.
- A service, tunnel or secret is missing from the panels: the file den loaded is not the one you edited. The Config panel shows its path.
- A command stops with `invalid config:` and a `file:line: message` line for each error: fix those lines. `den` itself opens anyway, on the Config panel with the errors in front.

## See also

- Files and folders
- Environments
- Services
- Editing den.yaml
- Doctor
- Command line
- `docs/init/README.md`
- `docs/schema/README.md`
