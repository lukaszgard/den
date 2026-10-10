# Writing a den runbook

A runbook is a script or tool the user runs from den — its Runbooks panel, `den run
<name>`, or an agent through den's MCP tools — with den's connection context set up
for it. This guide is what you need to write one that den loads and runs well.

## Shape: one folder per runbook

A runbook is a folder in one of den's runbooks directories (`runbooks.dirs` in den.yaml;
`runbook_guide` lists them) holding a `runbook.yaml`:

```
rds-status/
  runbook.yaml     # required: how den runs it
  README.md        # what it does, when to run it, how to check the result
  run.sh           # or Cargo.toml + src/, main.go, main.py, package.json …
```

Everything the runbook needs lives in its folder. A folder without `runbook.yaml` is
ignored, so a shared `lib/` folder next to the runbooks is fine.

## runbook.yaml

The same keys as an entry of `runbooks.items` in den.yaml. den reads it **strictly**:
a key it does not know makes it skip the whole runbook, so use only these.

```yaml
name: RDS status — eu-staging      # optional: the folder name otherwise; unique across den
description: Status, engine and size of every RDS instance
command: ./run.sh                # required; run through sh -c, inside this folder
env: eu-staging                    # an environment from runbook_guide: sets AWS_PROFILE, region …
timeout: 2m                        # Go duration; the run is stopped when it elapses
confirm: true                      # den shows the command and asks before every run
params:                            # asked for before each run, reach the script as DEN_PARAM_<NAME>
  - name: cluster
    description: Which cluster
    options: [orders, payments]    # a fixed list; omit for free text
    default: orders
    required: true
  - name: token
    secret: true                   # masked when typed, redacted from output and history
env_vars:                          # extra variables; they win over everything den sets
  AWS_PAGER: ""
retry:                             # re-run until exit 0 (polling); needs max_attempts or timeout
  until_success: true
  interval: 15s
  max_attempts: 20
cwd: data                          # working directory, relative to the folder; the folder by default
history: 20                        # finished runs den keeps in logs/ in the folder (log-01.log, …); 0 keeps only the last one
```

## How den runs it

- `command` runs through `sh -c` with the folder as its working directory, so `./run.sh`,
  `cargo run --release`, `go run .` and `uv run main.py` all work as written.
- **The exit code is the result**: 0 is success, anything else a failure. Exit non-zero
  when the check fails, so `den run` and CI see it.
- stdout and stderr are captured and shown as the run's log. Print short, readable lines;
  a table at the end is fine.
- **There is no stdin.** Never prompt; declare a `param` instead. `confirm: true` is how a
  runbook asks before it does something.
- Runs are not interactive terminals: no progress bars that need a TTY. den sets
  `FORCE_COLOR=1` and `CLICOLOR_FORCE=1` (unless `NO_COLOR` is set), so colour is welcome:
  a script that decides on `isatty()` should also honour those two. Only colour and style
  codes reach the log; cursor moves and other escape sequences are dropped.

## What den sets

| Variable | From |
|---|---|
| `AWS_PROFILE` | the `env`'s AWS profile — use the SDK's or CLI's default credential chain, never keys |
| `AWS_REGION`, `AWS_DEFAULT_REGION` | the `env`'s region |
| `DEN_ENV`, `DEN_REGION`, `DEN_ENV_NAME` | the `env`'s environment, region and key |
| `DEN_EC2_INSTANCE_ID` | the `env`'s bastion instance |
| `DEN_RUNBOOK`, `DEN_CONFIG` | the runbook's name, den.yaml's path |
| `DEN_RUNBOOK_STATE` | a folder of the runbook's own that outlives its runs: keep caches, virtualenvs and build output there |
| `DEN_PARAM_<NAME>` | each param, upper-cased |
| `FORCE_COLOR`, `CLICOLOR_FORCE` | `1` in a run, so output keeps its colours in the Logs and History tabs; not set when `NO_COLOR` is |
| `DEN_PORT_<TYPE>_<NAME>` | the local port of a connection that is up — only when run from den's TUI, or from a `den mcp` session that connected it |

A script that needs a tunnel must fail with a clear message when its `DEN_PORT_*` variable
is missing: `: "${DEN_PORT_RDS_ORDERS:?connect orders in den first}"`.

## Rules

- **Never print or log credentials**: tokens, passwords, keys, `aws configure export-credentials`
  output. den redacts secret params, nothing else.
- **Read-only by default.** Anything that changes state (deletes, restarts, writes data,
  rotates secrets) gets `confirm: true` and a `timeout`.
- **One environment per runbook**, from `runbook_guide`. Never hard-code account ids,
  hosts or profiles that the `env` already gives.
- **Prefer the AWS CLI or the official SDK** with the default credential chain; den has
  already put the profile and region in the environment.
- Write a `README.md`: what it does, when to run it, what a good and a bad result look
  like, and anything it changes.

## Per language

- **bash**: `run.sh` starting `#!/usr/bin/env bash` and `set -euo pipefail`, executable
  (`chmod +x`); `command: ./run.sh`.
- **Python**: `main.py` plus `pyproject.toml`; `command: uv run main.py` (uv creates the
  environment on first run), or `python3 main.py` with only the standard library.
- **Go**: `go.mod` and `main.go`; `command: go run .`.
- **Rust**: `Cargo.toml` and `src/main.rs`; `command: cargo run --quiet --release` (the
  first run compiles, so give it a `timeout` that allows that), and a `.gitignore` with
  `target/`. Use the `aws-config` and `aws-sdk-*` crates with `BehaviorVersion::latest()`:
  they read `AWS_PROFILE` and `AWS_REGION` from the environment.
- **Node**: `package.json` and `index.js`/`index.ts`; `command: npx --yes tsx index.ts` or
  `node index.js`, with the AWS SDK v3 clients.

Build it once yourself (`cargo build`, `go build`, `uv sync`) so the first run from den
does not fail on a compile error.

## Shared in a repository

A runbook may end up in a **runbook source**, a git repository other people add to their
den. Write it so it works there too:

- Use a plain env name (`prod`, `int`) and say in the README which account it means: each
  user maps it to their own environment with `env_map`.
- Don't set `shell`, `editor`, `agent` or `history`, nor `env_vars` for `AWS_*`, `PATH` or `DEN_*`:
  den ignores them for a runbook from a source.
- Its folder is replaced on every update of the source, so anything a run builds or caches
  goes in `$DEN_RUNBOOK_STATE` (`CARGO_TARGET_DIR="$DEN_RUNBOOK_STATE/target"`, a virtualenv
  there), not in the folder.

## Finish

1. Call den's `validate_runbook` with the folder: it loads it exactly as den will and
   reports every problem. Fix them and call it again until it reports none.
2. Tell the user how to run it: the Runbooks panel (select it, `r`), or `den run "<name>"`.
   Run it yourself only if the user asks (`run_runbook`, when den allows it).
