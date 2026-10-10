# From the Runbooks panel

## What it is

In den's Runbooks panel, `n` asks for the new runbook's name and, when an agent is
installed, *Ask an agent to write it (optional)*. The answer box grows with the text and scrolls once the
pane is full; `↑` or `shift+tab` goes back to the name. With two or more agents in
`runbooks.agents`, an *Agent* row under it is moved to with `tab` and changed with `←`/`→`. With an answer, the agent runs in a terminal tab of the
new runbook's view, in its folder, so you watch it work inside den. One `esc` is the
agent's there; `esc` twice or `ctrl+t` gives den the keys back. When the agent quits, den reads the folder
again and the runbook is in the list.

## Configuration

[`den.yaml`](den.yaml) is the same shape as the [CLI demo](../cli/den.yaml):

| Key | Meaning |
|---|---|
| `menu_order` | `[runbooks, help]`: the panel this demo uses |
| `runbooks.dirs` | `runbooks`, next to this file (git-ignored) |
| `runbooks.template` | `../template`, with a `.claude/settings.json` that pre-allows the writes **for this recording** |
| `runbooks.agent` | Which agent `n` starts: `claude`, `codex`, `auto` or a `runbooks.agents` name |

## Prerequisites

Claude Code (or Codex), logged in. No AWS.

## Usage

```bash
den -c docs/runbooks/agents/panel/den.yaml
```

Runbooks → `n` → a name → the request for the agent. Leave the second answer empty to get the
script in your editor instead. When Claude is done, `esc` `esc` (or `ctrl+t`), then `r`
runs it. Re-render:
`vhs docs/runbooks/agents/panel/agent-panel.tape`.
