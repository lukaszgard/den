# Drafts and saved requests

## What it is

What you type for an agent in the Runbooks panel, the request of `n` (up to 4000
characters) and the change `E` asks for, is kept on disk while you type it. Two things
come of that:

- **Drafts.** An unfinished request survives `esc`, quitting den and a crash. `esc` on a
  form with something in it asks *Keep as a draft?*; a kept draft is a row of the Runbooks
  list, and `enter` on it, or `n`, opens the form again where you left it. Step away to
  check something, then come back to it.
- **Saved requests.** Once `enter` has created the runbook (or started the change), the
  request is kept with it. An agent that fails is restarted with `enter` on its tab, and
  den asks whether to send it the request again instead of starting it empty. After den
  itself restarted, `E` on the runbook opens filled with the request.

Requests stay on this machine, in den's state folder, readable only by you: a request may
hold a password. `den mcp` and `--json` never show them.

## Configuration

Nothing is needed: drafts are kept for 30 days, and a saved request until its runbook is
deleted. `runbooks.drafts` changes both:

```yaml
runbooks:
  drafts:
    keep_request: until-exit        # until-deleted (default), until-exit or never
    days: 7                         # 0 keeps drafts until you delete them
```

| Key | Required | Default | Meaning |
|---|---|---|---|
| `drafts.keep_request` | no | `until-deleted` | What happens to a request once its runbook is created or its change started. `until-deleted`: kept until the runbook is deleted (`D`, `den runbook delete`). `until-exit`: removed when the agent exits cleanly by itself; a failed agent, or a tab den closed, keeps it. `never`: removed as soon as the agent has started; an agent that failed to start keeps it |
| `drafts.days` | no | `30` | Days an untouched draft is kept. Older drafts are deleted the next time the list is read. `0` keeps them until you delete them. A saved request follows `keep_request`, not `days` |

[`den.yaml`](den.yaml) next to this page is the configuration of the recording.

## Prerequisites

- An agent CLI den can start (Claude Code, Codex, OpenCode, …) installed: without one, `n`
  asks only for a name and `E` starts nothing. The recording types requests but sends none,
  so the agent need not be logged in.
- A home directory: drafts live in the state directory. Without one den keeps none, and
  `esc` closes a form as it always did.
- No AWS, no IAM permissions.

## Usage

**Keep a draft.** `n`, a name, a request, then `esc`:

```
╭─────────────────────────────────────────────────────────────────────╮
│  ┃ New runbook name                                                 │
│  ┃ > Rotate app password                                            │
│                                                                     │
│    Ask an agent to write it (optional)                              │
│    > rotate the app user's password in Secrets Manager              │
│                                                                     │
│  Keep as a draft? y: keep  n: discard  esc: back to it              │
╰─────────────────────────────────────────────────────────────────────╯
```

`y` keeps it, `n` deletes it, `esc` goes back to the form. An empty form closes without
asking. The form also saves itself a second after you stop typing, and when den quits
(`ctrl+c`, `q`), so a crash loses at most the last second. `E`'s question works the same
way.

**Find it again.** A draft is a row at the top of the list, TYPE `draft`, the first line of
its request as the description; `/` finds it by its words. A draft of a change reads
`Greet (change)`:

```
   STATUS       NAME                   TYPE     DESCRIPTION
 ▸ ✎ 5m ago     Rotate app password    draft    rotate the app user's password in Secrets Manager
   ✎ 2d ago     Greet (change)         draft    ask for the name as a parameter
   ○ never      Greet                  sh       Says hello
```

| Key on a draft | What it does |
|---|---|
| `enter` | Opens its form again, filled in: `n`'s for a new runbook, `E`'s in the runbook's view for a change |
| `D` | Deletes it, after *Delete the draft "…"? y/n* |
| `r`, `x`, `m`, `E` | Say it is a draft: it runs nothing |

`n` with drafts of new runbooks first offers them, newest first, under *New runbook*; with
none it opens the form at once. `E` on a runbook with a draft of a change opens that draft.

**Give a failed agent its request again.** `enter` on the agent's exited tab asks:

```
Restart the agent

It was started with:
  rotate the app user's password in Secrets Manager

Send the request again? y: send it  n: start empty  esc: cancel
```

`y` (or `enter`) sends it, `n` starts the agent without it, `esc` restarts nothing. With no
saved request (`keep_request: never`, or already removed) the tab restarts at once.

**After den restarted.** Agent tabs live only in memory. `E` on a runbook with a saved
request opens *What should change in NAME?* filled with it: `enter` sends it again, or
change it first.

**Where they are.** One JSON file per draft or saved request, in
`~/.local/state/den/runbook-drafts/` (`$XDG_STATE_HOME/den/runbook-drafts`, or
`%LOCALAPPDATA%\den\runbook-drafts` on Windows), the folder `0700` and each file `0600`.
`den paths` lists it:

```
runbook drafts             exists   ~/.local/state/den/runbook-drafts  (requests typed for an agent; may hold secrets)
```

Deleting a runbook deletes its saved requests and drafts of changes to it, so a runbook
given the name later inherits none of them. A saved request whose runbook folder was
removed outside den is dropped when den starts; one whose `runbook.yaml` no longer loads
is kept, since that is when it is wanted.

**Limits.** With `until-exit`, an agent you quit yourself (`/exit` in Claude Code) counts as
a clean exit and takes the request with it. Drafts have no command line: `den runbook new`
takes the request as an argument already. A draft saved by another den shows in the list
after a refresh (leaving a runbook's view, or changing a draft here).

To record this page's demo: `make tape TAPE=drafts`.
