---
name: feature-docs
description: This skill should be used when the user asks to "document the feature", "write the docs for", "add a tape", "add a VHS recording", or when finishing any new den feature or significant change, which must ship with docs/<feature>/README.md and a .tape.
---

# Document a den feature

Every new feature or significant update ships with both files below; a change that
alters behaviour also updates the places in step 3.

## 1. `docs/<feature>/README.md`

Exactly these sections, in this order:

- **What it is** — what it does and the problem it solves, in a few sentences.
- **Configuration** — the `den.yaml` snippet with every key, and a table explaining
  each (required or not, default, meaning). "None" is a valid answer.
- **Prerequisites** — tools and versions, IAM permissions (exact action names), env vars.
- **Usage** — commands or TUI keys, with realistic output.

Use placeholder names (`orders`, `example.com`, `i-0123456789abcdef0`), never real
hosts, account IDs or profile names.

## 2. `docs/<feature>/<feature>.tape`

A VHS script for this feature only (not an app tour), starting with the `Output` lines
and the shared settings (window, font, typing speed; tapes run from the repo root):

```
Output docs/<feature>/<feature>.gif
Output docs/<feature>/<feature>.mp4

Source docs/tape-settings.tape
```

Then navigate, show the configuration, exercise the key interactions. Pace every step by
the tiers in AGENTS.md (Feature documentation); name the tier on each `Sleep`. Anything
slow (a connect, a login, a run) is a `Wait+Screen` on a string grepped from the code,
never a long blind `Sleep`. Add a comment saying what the recording needs (AWS login, a
sandbox account). Check the tape with `vhs validate <tape>`. Tapes are rendered by a
person with `make demo` (or `make tape TAPE=<name>` for one), not by an agent: never run
`vhs <tape>`.

## 3. Also update

- `docs/help/<feature>.md` — the feature's in-app Help topic, listed in
  `docs/help/README.md`. Same template for every topic (AGENTS.md has it; a new topic
  copies the shape of `docs/help/rds.md`): what it is, what you can do, before you
  start, configure (every key as a bullet, then several examples showing different
  options), usage (optional: a step-by-step for a common task), limitations, when it
  goes wrong, see also. No tables, links, images or key
  lists, and each paragraph or bullet on one source line. A feature that changes an
  existing topic updates it; new commands go into `command-line.md`, new keys into the
  pane's shortcuts pop-up.
- `site/data/sections.yaml` — the new `docs/<feature>/README.md` in the group it
  belongs to, so the documentation site shows it (`make site-build` fails until it
  is listed; `make site` previews it).
- `README.md` — the feature list and the supported-services table. The config tables are
  generated (`docs/config`, `docs/cli`) by `make generate`; do not write them by hand.
- `den.yaml` — a commented example of any new key; it must still load
  (`go test ./internal/config -run TestRepoConfig`).
- `make generate` after config struct or command changes (schema, `docs/config`, `docs/cli`).
