# Runbook sources

![runbook-sources.gif](runbook-sources.gif)

## What it is

A **runbook source** is a git repository of [runbook folders](../runbooks/README.md) — your
platform team's, a shared ops repository, a community collection — that den checks out
and lists beside your own runbooks. Its runbooks are named after the source:
`platform-ops/rds-status` runs the `rds-status` folder of the `platform-ops` source.

You add the repository once, in `den.yaml` or from den. You choose which of its runbooks
to install, update it when you want to, and copy any of its runbooks into your own folder
to change it.

A runbook from a source is **someone else's code, run with your AWS credentials**. den
treats it that way:

- **Nothing updates on its own.** A source is pinned to one commit, recorded in `den.lock`
  beside `den.yaml`. `den runbook source update` moves it, and says which runbooks changed.
- **It asks before its first run, and again after an update changed it.** den remembers
  the git tree of each runbook's folder you ran; new code means a new question, with the
  reason shown.
- **Your names win.** A source's runbook is always `<source>/<name>`, and if one of your own
  runbooks has that name, yours is kept.
- **Its env must be mapped to one of yours.** The author's `prod` is not necessarily your
  `prod`, even when the names match, so a runbook whose `env:` is not in the source's
  `env_map` is not listed.
- **It cannot change den's context or what `t` and `e` start.** A source runbook's
  `shell`, `editor` and `history` are ignored, and so are `env_vars` that set `PATH`,
  `AWS_*` or `DEN_*`. Its `cwd` must stay inside the repository.
- **Its text is cleaned.** Terminal escape sequences are stripped from its name,
  description, parameters and README.
- **AI agents need a second opt-in.** `den mcp` hides source runbooks unless
  `mcp.allow_source_runbooks` is set, and even then asks you before every run.

## Configuration

```yaml
runbooks:
  dirs:
    - ~/.config/den/runbooks              # your own runbooks; den runbook fork copies into the first

  # Optional: the only repositories a source may come from (host/owner/repo, * wildcards).
  allow_sources:
    - ghe.example.com/platform/*

  sources:
    - name: platform-ops                  # its runbooks are platform-ops/<name>
      url: git@ghe.example.com:platform/den-runbooks.git
      ref: v1.4.0                         # tag, branch or commit; empty: the default branch
      path: runbooks                      # the folder holding the runbook folders; empty: the root
      include: ["rds-*", "bastion-health"]  # which runbook folders to show; empty: all
      exclude: ["rds-legacy"]             # hide some that include shows
      env_map:                            # the env names its runbooks use → your environments
        prod: orders-prod
        int: orders-int

mcp:
  allow_runbooks: true
  allow_source_runbooks: false            # agents may also run source runbooks, after asking you
```

`runbooks.sources[]`:

| Key       | Required | Default        | Meaning |
|-----------|----------|----------------|---------|
| `name`    | yes      | –              | Prefix of its runbooks' names: lower case letters, digits and dashes |
| `url`     | yes      | –              | The repository: `https://host/owner/repo`, `ssh://…`, `git@host:owner/repo.git`, `host/owner/repo`, or `owner/repo` on GitHub. Never put a token or password in it: den rejects one. Credentials come from git (a credential helper, your SSH key or agent) |
| `ref`     | no       | default branch | The tag, branch or commit to check out. A tag or a commit stays put; a branch moves on `update` |
| `path`    | no       | the root       | The folder of the repository holding the runbook folders. A `runbook.yaml` in that folder itself is one runbook, named after the source |
| `include` | no       | all            | Runbook folder names to show, with `*` wildcards. `den runbook install` and `uninstall` edit it |
| `exclude` | no       | none           | Runbook folder names to hide, with `*` wildcards |
| `env_map` | no       | none           | The source's env names, mapped to entries of your `environments:`. A runbook using an unmapped env is not listed, and the Runbooks panel's `w` says why |

`runbooks.allow_sources` (optional) lists the repositories sources may come from, as
`host/owner/repo` patterns with `*`; den refuses to add, and den.yaml fails to load with,
a source outside them. `mcp.allow_source_runbooks` (default `false`) lets agents see and
run source runbooks through `den mcp`; it needs `mcp.allow_runbooks` too.

### den.lock

`den runbook source update` writes `den.lock` beside `den.yaml` (beside the file a
symlinked `den.yaml` points at):

```yaml
# den.lock — written by den runbook source update: the commit each runbook
# source is checked out at. Commit it beside den.yaml so that everyone sharing
# the config runs the same runbooks (den runbook source sync).
sources:
  platform-ops:
    url: git@ghe.example.com:platform/den-runbooks.git
    ref: v1.4.0
    commit: 3f2a1c9d5e8b7a6f4c3d2e1f0a9b8c7d6e5f4a3b
    updated: 2026-10-03T09:00:00Z
```

Commit it with a shared `den.yaml`: a teammate runs `den runbook source sync` (or `S` in
the panel) and gets exactly those commits.

### Where things live

| What | Where |
|------|-------|
| Mirrors and checked-out trees | `$XDG_DATA_HOME/den/sources/` (`~/.local/share/den/sources/`, `%LOCALAPPDATA%\den\sources\` on Windows) |
| The runbooks you ran, by git tree | `$XDG_STATE_HOME/den/runbook-trust.json` (`~/.local/state/den/`) |
| `DEN_RUNBOOK_STATE` folders | `$XDG_DATA_HOME/den/runbook-state/<runbook>/` |

Each commit is checked out once, as a plain copy without `.git`, and never changes after:
an update makes a new tree beside the old one, so a runbook still running keeps its files.
The current tree and the one before it are kept. To change a runbook for good, `fork` it.

A checkout is replaced by every update, so a runbook that builds something — a virtualenv,
a compiled binary, a cache — keeps it in `$DEN_RUNBOOK_STATE`, a folder of its own that
every runbook (yours too) gets and that outlives its runs.

### Writing a repository of runbooks

A source is an ordinary repository of runbook folders, each with its `runbook.yaml`:

```
den-runbooks/
  runbooks/              ← path: runbooks
    rds-status/
      runbook.yaml
      run.sh
      README.md          ← shown in the About tab
    bastion-health/
      runbook.yaml
      check.py
  lib/                   ← not a runbook: no runbook.yaml
```

- Use **plain env names** (`prod`, `int`) and say in the README which account each means:
  every user maps them to their own environments with `env_map`.
- Don't set `shell`, `editor` or `history`; den ignores them in a source.
- Don't rely on `env_vars` for `AWS_*`, `PATH` or `DEN_*`; den ignores those too.
- Keep anything a run builds in `$DEN_RUNBOOK_STATE`, not in the runbook's folder.
- Tag releases (`v1.4.0`) so users can pin one.

`den runbook new` and an agent with `den mcp` write runbooks in this shape already.

## Prerequisites

- **git** on `PATH` (`den doctor --for runbook-sources` checks it and prints how to install
  it). den runs it without a terminal: a repository that needs a password must get it from a
  credential helper (`git config --global credential.helper osxkeychain`, Git Credential
  Manager, `gh auth setup-git`) or an SSH key in your agent. A prompt is never shown; it
  fails with git's own message instead.
- Network access to the repository when you add, update or sync. A source behind a VPN
  needs the VPN up for those commands only; the runbooks already checked out keep working
  without it.
- No IAM permissions of its own: the runbooks need whatever their scripts use.

## Usage

### From the command line

```
$ den runbook source add git@ghe.example.com:platform/den-runbooks.git \
    --name platform-ops --ref v1.4.0 --path runbooks --env-map prod=orders-prod
added platform-ops to den.yaml
platform-ops  checked out 3f2a1c9  (v1.4.0)
  + bastion-health           new
  + rds-status               new
New and changed runbooks ask before their next run.
Its runbooks are listed as platform-ops/<name>: den run --list, or den runbook browse platform-ops

$ den runbook browse platform-ops
platform-ops · ghe.example.com/platform/den-runbooks @ 3f2a1c9 (v1.4.0)

  ✓ bastion-health       platform-ops/bastion-health      Check the SSM bastion is online
  ✓ rds-status           platform-ops/rds-status          Cluster status and engine version

  ✓ installed · den runbook install platform-ops/<folder> · den runbook uninstall platform-ops/<folder>

$ den run platform-ops/rds-status
Runbook: platform-ops/rds-status
  Cluster status and engine version
  from:    platform-ops · ghe.example.com/platform/den-runbooks @ 3f2a1c9
  note:    new from platform-ops: read it before its first run
  env:     orders-prod
  …
Run this? [y/N]
```

| Command | What it does |
|---------|--------------|
| `den runbook source add <url> [--name N] [--ref R] [--path P] [--env-map theirs=yours]… [--no-fetch]` | Add a source to `den.yaml` (keeping the rest of the file as written), check it out and pin it |
| `den runbook source list [--json]` | Each source: repository, ref, commit, how many runbooks are installed, what is wrong |
| `den runbook source update [name…]` | Fetch and move to the newest commit of the ref; prints each runbook added (`+`), changed (`~`, with git's stat) or removed (`-`) |
| `den runbook source sync` | Check out the commits `den.lock` pins, fetching only what is missing |
| `den runbook source remove <name> [--yes]` | Remove it from `den.yaml` and `den.lock` and delete its checkout, with what den kept for each of its runbooks: runs, state folders, your reviews |
| `den runbook browse <source> [--json]` | Every runbook in a source, installed or not |
| `den runbook install <source>/<runbook>` | Show one of its runbooks (edits `include`/`exclude`) |
| `den runbook uninstall <source>/<runbook>` | Hide one |
| `den runbook delete <source>/<runbook> [--yes]` | Uninstall it and forget it: its runs, its `DEN_RUNBOOK_STATE` folder and your review of it. Installed again, it asks before it runs |
| `den runbook fork <source>/<runbook> [--dir D]` | Copy it into your first runbooks folder as your own, with `env:` set to your environment |

Outside a terminal, a source runbook that asks first needs `den run <name> --yes`; that
`--yes` counts as having reviewed it. `den run --list --json` gives each source runbook's
`from`, `commit` and, when it asks first, `note`.

### In the Runbooks panel

Source runbooks are rows like your own, named `<source>/<name>`, with `!` while they ask
before running. The confirmation says where one comes from and why it asks:

```
 Confirm before running

 Runbook: platform-ops/rds-status
          Cluster status and engine version
 From:    platform-ops · ghe.example.com/platform/den-runbooks @ 3f2a1c9
 Why:     changed in platform-ops since you ran it at 1b2c3d4
 Env:     orders-prod
```

`s` opens the sources pop-up:

| Key | Action |
|-----|--------|
| `↑` `↓` | Select a source |
| `enter` | Its runbooks: `space` installs or uninstalls the one selected, `esc` goes back |
| `u` | Update the source (in the background; the pop-up says what moved) |
| `S` | Check out what `den.lock` pins, for every source |
| `a` | Add a source: its URL, then optionally its name, ref and path |
| `d` | Remove the source, after asking |
| `esc` | Close |

In a source runbook's view, `e` offers to **copy it into your runbooks** instead of opening
its checkout, then opens the copy in your editor; `t` opens a shell in its folder as usual.

Its Files tab shows the checkout's files, so you can read a script before its first run,
and `e` there opens a file **of the checkout itself** in your editor, to try a fix in place. When the editor closes with the file changed, den
forgets that you ran the runbook, so its next run asks first ("edited here"), and says
that the edit is lost when the source updates to another commit. The footer of Files says
so too. Running it after that confirmation trusts the edited files.
Problems with a source (not checked out, `den.yaml` asking for another ref, an unmapped
env, a key from a newer den) are discovery warnings: `w` shows them.

### den doctor

`den doctor` needs git once `runbooks.sources` is set, and reports each source:
`checked out at 3f2a1c9 (v1.4.0)`, or what is wrong and the command that fixes it.
