# Runbook sources

Shared runbooks from git, pinned and asked about

## What it is

A runbook source is a git repository of runbook folders (your platform team's, a shared ops repository) that den checks out and lists beside your own runbooks, as `<source>/<name>`. It is someone else's code run with your AWS credentials, so den pins it to one commit, asks before the first run of each runbook and again after an update changed it, and maps its environment names to yours.

## What you can do

- Add a repository from `den.yaml`, from the Runbooks panel or with `den runbook source add`, pinned to a tag, a branch or a commit.
- Choose which of its runbooks to show with `include` and `exclude`, or `den runbook install` and `uninstall`.
- Delete one with `D` (`den runbook delete SOURCE/NAME`): it is uninstalled, and den forgets its runs, its state folder and your review of it; the checkout stays, shared with the source's other runbooks.
- Update it when you decide to, and see which runbooks changed; `den.lock` records the commit, so a team sharing `den.yaml` runs the same code (`den runbook source sync`).
- Map the source's environment names to your environments: its `prod` is not assumed to be yours.
- Copy a runbook into your own folder to change it (`den runbook fork`).
- Keys: press `h` in the Runbooks panel.

## Before you start

- `git` (`den doctor --for runbook-sources`), and access to the repository through your SSH key, agent or git credential helper.
- The environments its runbooks need, in your `environments:`.

## Configure

Under `runbooks:`, a `sources:` list and, optionally, `allow_sources`.

- `name` (required): the prefix of its runbooks; lower case letters, digits and dashes.
- `url` (required): `https://host/owner/repo`, `git@host:owner/repo.git`, `host/owner/repo`, or `owner/repo` on GitHub. Never a token in it: den refuses one.
- `ref`: a tag, branch or commit; the default branch when unset. A branch moves on update, a tag or commit stays.
- `path`: the folder of the repository holding the runbook folders; the root when unset.
- `include`, `exclude`: runbook folder names, with `*`, to show or to hide.
- `env_map`: the source's environment names mapped to your environments; a runbook using an unmapped one is not listed.
- `allow_sources`: the only repositories a source may come from, as `host/owner/repo` patterns with `*`.

A team repository, pinned to a release, with its environments mapped to yours:

```yaml
runbooks:
  allow_sources:
    - ghe.example.com/platform/*
    - github.com/acme-ops/*
  sources:
    - name: platform-ops
      url: git@ghe.example.com:platform/den-runbooks.git
      ref: v1.4.0
      path: runbooks
      env_map:
        prod: prod
        int: dev
```

Only some of a public collection's runbooks, following its main branch:

```yaml
runbooks:
  sources:
    - name: acme-tools
      url: acme-ops/den-runbooks
      ref: main
      include: ["rds-*", "bastion-health"]
      exclude: ["rds-legacy"]
      env_map:
        prod: prod
```

Then, from a shell:

```
den runbook source update
den runbook source sync
den runbook fork platform-ops/rds-status
```

## Limitations

- Nothing updates by itself, and every changed runbook asks again before its next run.
- A source's runbook cannot set `shell`, `editor` or `history`, nor `env_vars` for `PATH`, `AWS_*` or `DEN_*`; den ignores them.
- An update replaces the checkout: anything a runbook builds belongs in `$DEN_RUNBOOK_STATE`.
- AI agents do not see source runbooks unless `mcp.allow_source_runbooks` is on, and even then every run asks you.
- If one of your runbooks has the same name, yours wins.

## When it goes wrong

- A runbook of the source is not listed: its `env` is not in `env_map`; the panel's warnings say so.
- `… is not in runbooks.allow_sources`: the repository is outside the patterns; den refuses to load the file.
- The update fails to fetch: git cannot reach the repository with your credentials; try the URL with `git ls-remote`.

## See also

- Runbooks
- AI agents (MCP)
- `docs/runbook-sources/README.md`
