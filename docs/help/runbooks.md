# Runbooks

Your scripts, run with den's AWS context

## What it is

A runbook is one of your own scripts or commands (shell, Python, Go, anything) that den runs with the context it already knows: the AWS profile, region and bastion of an environment, and the local port of every tunnel that is up. Each runbook is a folder with a `runbook.yaml` next to its scripts; short ones can be written straight into `den.yaml`. It is not a workflow engine: the logic stays in your script.

## What you can do

- Run a runbook from the Runbooks panel, or from a shell with `den run NAME`, which exits with the script's own code.
- Get `AWS_PROFILE`, `AWS_REGION`, `DEN_ENV`, `DEN_EC2_INSTANCE_ID` and the rest exported from the runbook's environment, and `DEN_PORT_<TYPE>_<NAME>` for every connection that is live.
- Ask for parameters before a run, with defaults, choices, required ones, and secret ones that are masked and kept out of logs.
- Ask before running, stop after a timeout, and re-run until the script succeeds.
- Chain runbooks into sequences that stop at the first failure.
- Look back at past runs: one log file per run, kept in the runbook's folder, with the run's status and parameters.
- Open a runbook like a service: its log, its history, what den hands it, and terminals that open in its folder with the same environment.
- Create a runbook from a template, or have an agent CLI write it from a description (`den runbook new NAME "…"`): Claude Code, Codex, OpenCode, Copilot CLI, Qwen Code, Droid, Pi or Kimi Code.
- Change a runbook with an agent: `E` asks what to change and starts the runbook's own `agent` (`claude --model opus`, say) in a tab of its folder.
- Keep what you type for an agent: `esc` asks to keep it as a draft, a kept draft is a row of the list that `n` offers again, and an agent restarted on its tab can be sent its request again.
- Delete a runbook or sequence for good with `D` (`den runbook delete NAME`): its folder or den.yaml entry, its runs and its state folder, after a question that lists all of it.
- Keys: press `h` in the Runbooks panel or a runbook's view.

## Before you start

- Whatever your scripts call (`aws`, `python3`, `flyway`, …) on your PATH.
- No extra IAM permission: den adds nothing of its own to what your scripts do.
- For an agent to write runbooks: one of the agent CLIs installed and logged in; `den doctor --for <kind>` (`--for opencode`, say) shows how to install it.

## Configure

A `runbooks:` section. Folders are found in `dirs`; `items` are runbooks written inline.

- `dirs`: folders holding runbook folders (each with a `runbook.yaml`); relative paths are read from `den.yaml`'s folder.
- `rescan` (default `true`): notice runbook folders added, changed or removed while den runs.
- `template`: a folder copied for every new runbook.
- `agent` (default `auto`): a kind (`claude`, `codex`, `opencode`, `copilot`, `qwen`, `droid`, `pi` or `kimi`), `auto` for the first one installed in that order (with `agents`, the first entry), or the `name` of an entry in `agents`.
- `agents[]`: the command lines that write a runbook; `name` (unique, not `auto`, no spaces at either end) and `command` (required), `kind`.
- `command` is split on whitespace, with no quotes; its first word is the program, and den adds its own arguments after yours.
- `kind` (one of the kinds above) is read from the program (`opencode`, with a path or `.exe`) or from `ollama launch <kind>`, where `ollama launch copilot-cli` is `copilot`; set it for any other wrapper.
- Claude Code, Codex, OpenCode and Copilot CLI get den's MCP server for the session; Qwen Code, Droid, Pi and Kimi Code do not, and check their work with `den runbook validate` from their shell.
- For `ollama launch …` den adds `--` before its arguments, so they reach the launched tool.
- With `agents`, the first entry is the default unless `agent` names another; with two or more, `n` offers a choice.
- `defaults`: `history` (runs kept, default 20), `about` (the About tab, default `true`), `shell`, `editor` and `agent`, for every runbook.
- `agent` (in a `runbook.yaml`, an item or `defaults`): what `E` changes the runbook with; a command line such as `claude --model opus`, or a kind, `auto` or the name of an `agents` entry. Default: the top-level `agent`.
- `items[]`: `name` and `command` (required), `description`, `env`, `cwd`, `env_vars`, `confirm`, `timeout`, `params`, `retry`, and the `defaults` keys.
- `params[]`: `name`, `description`, `default`, `options`, `required`, `secret`; a parameter reaches the script as `DEN_PARAM_<NAME>`.
- `retry`: `until_success`, `interval`, `max_attempts`, `timeout`; one of the last two is required.
- `sequences[]`: `name` and `steps` (required, runbook names in order), `description`, `confirm`.
- `sources`, `allow_sources`: git repositories of runbooks; see Runbook sources.
- `drafts`: what `n` and `E` keep of a request typed for an agent; `keep_request` (`until-deleted`, the default, keeps it until the runbook is deleted; `until-exit` until the agent exits cleanly by itself; `never` until the agent has started) and `days` (how long an untouched draft is kept, default 30; `0` keeps it until you delete it).

Claude Code with a chosen model, and Codex with another:

```yaml
runbooks:
  agent: Claude Opus
  agents:
    - name: Claude Opus
      command: claude --model opus
    - name: Codex GPT
      command: codex --model gpt-5.4
```

Claude Code through Ollama; `kind` is read from `ollama launch claude`, and den puts `--` before its own arguments:

```yaml
runbooks:
  agents:
    - name: Ollama GLM
      command: ollama launch claude --model glm-5:cloud
```

OpenCode with a chosen model, and Copilot CLI; both get den's MCP server for the session:

```yaml
runbooks:
  agent: OpenCode Sonnet
  agents:
    - name: OpenCode Sonnet
      command: opencode --model anthropic/claude-sonnet-4-5
    - name: Copilot CLI
      command: copilot
```

Qwen Code and Pi through Ollama; they check their work with `den runbook validate`:

```yaml
runbooks:
  agents:
    - name: Ollama Qwen
      command: ollama launch qwen
    - name: Ollama Pi
      command: ollama launch pi
```

A wrapper den cannot read the agent from needs `kind`:

```yaml
runbooks:
  agents:
    - name: Team wrapper
      command: ./ai-claude --fast
      kind: claude
```

Your runbook folders, with every run's log kept longer and your own editor:

```yaml
runbooks:
  dirs:
    - ~/.config/den/runbooks
    - ./ops/runbooks
  defaults:
    history: 50
    editor: code --wait
```

Drafts kept for a week, and a request dropped once the agent has finished with it:

```yaml
runbooks:
  drafts:
    keep_request: until-exit
    days: 7
```

A one-line runbook written in `den.yaml`, run with the `prod` environment's profile and region after you confirm:

```yaml
runbooks:
  items:
    - name: rds status prod
      description: cluster status and engine version
      env: prod
      confirm: true
      command: aws rds describe-db-clusters --query 'DBClusters[].[DBClusterIdentifier,Status]' --output table
```

Parameters, a timeout, and a script that uses the tunnel den has open to the orders database:

```yaml
runbooks:
  items:
    - name: orders migration status
      env: dev
      timeout: 5m
      params:
        - name: target
          options: [info, validate]
          default: info
        - name: db_password
          secret: true
      command: flyway -url="jdbc:postgresql://127.0.0.1:${DEN_PORT_RDS_ORDERS_DEV}/orders" -password="$DEN_PARAM_DB_PASSWORD" "$DEN_PARAM_TARGET"
```

Colours: den asks every run for them (`FORCE_COLOR=1`, `CLICOLOR_FORCE=1`), so the Run and History tabs, and the Logs panel, show what a shell would. A tool that ignores both needs its flag; `NO_COLOR` in `env_vars` turns the request off:

```yaml
runbooks:
  items:
    - name: errors in the deploy log
      command: aws logs tail /app/deploy --since 1h | grep --color=always -E 'ERROR|$'
    - name: plain host listing
      env_vars:
        NO_COLOR: "1"
      command: aws ec2 describe-instances --query 'Reservations[].Instances[].InstanceId' --output text
```

Wait until a snapshot is ready, then run two runbooks in order:

```yaml
runbooks:
  items:
    - name: wait for snapshot
      env: prod
      command: aws rds describe-db-cluster-snapshots --db-cluster-snapshot-identifier nightly --query 'DBClusterSnapshots[0].Status' --output text | grep -qx available
      retry:
        until_success: true
        interval: 30s
        timeout: 30m
  sequences:
    - name: before the release
      confirm: true
      steps: [wait for snapshot, rds status prod]
```

A runbook that `E` changes with a stronger model than the rest, and one that says so in its own folder:

```yaml
runbooks:
  items:
    - name: restore drill
      description: restore last night's snapshot into a scratch cluster
      env: prod
      confirm: true
      agent: claude --model opus
      command: ./drill.sh
```

A `runbook.yaml` in a folder takes the same keys as an item; its `command` runs inside the folder:

```
name: rotate app password
command: ./run.sh
env: prod
confirm: true
```

## Usage

The quickest way to a new runbook is the Runbooks panel:

1. Press `n` and type a name, such as `Rotate app password`; the pop-up shows the folder it goes into, under the first of `runbooks.dirs`, and says at once when a runbook, a sequence or a folder has the name already.
2. Leave *Ask an agent to write it (optional)* empty: den creates the folder with a `runbook.yaml`, a `run.sh` that prints what den hands it, and a `README.md`, and opens `run.sh` in your editor.
3. Write the script and save it; the runbook is listed at once. `enter` opens it, `r` runs it.

The same from a shell; `--dir` puts the folder somewhere else:

```
den runbook new "Rotate app password"
den run "Rotate app password"
```

Have an agent CLI write it for you, from a description:

- In the panel: answer *Ask an agent to write it (optional)* after `n`. The box grows with what you type and scrolls once the pane is full; `ctrl+j` or `alt+enter` starts a new line, and `↑` or `shift+tab` goes back to the name to change it. With two or more agents an *Agent* row sits under it: `tab` (or `↓` from the last row) moves to it, `←` and `→` choose, `enter` creates. The agent works in a terminal tab of the new runbook, and den reads the folder again when it quits.
- From a shell: `den runbook new "RDS status" "show the status of my RDS clusters in prod" --agent claude`.
- `--agent` takes `auto`, a kind such as `claude` or `opencode`, or the name of an entry in `agents`.
- Kimi Code takes no first prompt: den puts the request in its instructions, and you type `go` in its tab to start it.
- From Claude Code with den registered: `/mcp__den__new_runbook` followed by what it should do.

Change a runbook you already have:

1. Select it (or open its view) and press `E`: *What should change in NAME?*; the pop-up names the agent, `claude opus` for `agent: claude --model opus`.
2. Describe the change; `ctrl+j` starts a new line, `enter` starts the agent, `esc` drops the question. Empty starts the agent without a request.
3. The agent works in a terminal tab of the runbook's view, in its folder, and checks its work with den's `validate_runbook`, or `den runbook validate` for an agent without den's MCP server; den reads the folder again when it quits.

Or start from what you already have:

- A script of yours: give it a `# den:name` comment line and put it in one of `runbooks.dirs`; it is a runbook already. `m` in the panel (`den runbook migrate NAME`) then moves it into a folder of its own.
- A runbook from a team repository: `den runbook fork SOURCE/NAME` copies it into your own folder, to change as you like.
- Your own starting point: set `runbooks.template` to a folder, and every new runbook starts as a copy of it.

Keep a request for later, and give it back to a failed agent:

1. In `n`'s form (or `E`'s), press `esc` with something typed: *Keep as a draft?* `y` keeps it, `n` deletes it, `esc` goes back. The form also saves itself a second after you stop typing, and when den quits.
2. The draft is a row at the top of the list, TYPE draft. `enter` on it, or `n` and then the draft, opens the form where you left it; `D` deletes it.
3. `enter` creates the runbook and keeps the request with it. If the agent fails, `enter` on its exited tab asks *Send the request again?*: `y` sends it, `n` starts the agent empty.
4. After den itself restarted, `E` on the runbook opens filled with the request.

When a runbook has had its day, select it and press `D`, in the list or its view. The question lists what goes (its folder, script or den.yaml entry, its runs with their logs, its `DEN_RUNBOOK_STATE` folder), what stays, and the sequences that will stop loading without it. Only `y` deletes it; any other key keeps it. From a shell, `den runbook delete NAME` asks the same, and needs `--yes` outside a terminal.

## Limitations

- No branching, conditions, parallel steps, scheduling or remote execution: put that logic in the script.
- `den run` outside the dashboard has no tunnels, so it exports no `DEN_PORT_*`; a script that needs one should check for it.
- Qwen Code, Droid, Pi and Kimi Code have no den MCP tools: they run `den runbook validate` with their shell tool, which may ask before it runs. Kimi Code waits for you to type `go`.
- OpenCode reads den's config through `OPENCODE_CONFIG`, in place of one you set yourself for that session; your global and project configs still load.
- `E` works on a runbook with a folder of its own: it refuses a sequence, a runbook written in `den.yaml` and a one-script runbook (`m` moves it into a folder first). A runbook from a source offers the copy `e` offers, and ignores its own `agent`.
- An `agent` that is no `agents` name and starts none of the kinds (`./my-agent.sh`) gets a warning in the panel, and `E` refuses it: put the wrapper in `agents` with `kind`, and name it.
- A `runbook.yaml` is read strictly: an unknown key skips the runbook with a warning in the panel.
- Only runs started from the dashboard write to the Logs panel; `den run` and `den mcp` runs do not.
- A script's colours are shown, and kept in its log; other escape sequences (cursor moves, window titles) are dropped, so a progress bar that redraws shows its last line. A tool that prints plain text without a terminal needs its own colour flag. `o` opens a copy of the log without the colour codes.
- Terminals in a runbook's view are built on ConPTY on Windows and have not been tried there yet.
- A deleted runbook is gone for good: den keeps no copy. Only what was committed to git, when its folder is in a repository, can come back.
- Deleting a runbook from a source uninstalls it: its checkout is shared with the source's other runbooks, and `den runbook source remove` deletes that.
- A `den mcp` already running keeps listing a deleted runbook until it reads the folders again, and running it then fails.
- Drafts and saved requests stay on this machine, in den's state folder; `den mcp` and `--json` never show them. Without a home directory den keeps none.
- With `keep_request: until-exit`, an agent you quit yourself (`/exit`) counts as a clean exit and takes the request with it.

## When it goes wrong

- A runbook is missing from the panel: the panel's warnings name the reason, often an unknown key, an unknown `env`, or a name that clashes.
- `unknown env`: the runbook's `env` is not in `environments:`.
- A script cannot reach the database: the service is not connected in the dashboard, so `DEN_PORT_*` is not set.
- `D` says a runbook is running: cancel it with `x`, or let it finish, here or in the other den (`den run`, `den mcp`) running it.

## See also

- Runbook sources
- Environments
- AI agents (MCP)
- Command line
- `docs/runbooks/README.md`
