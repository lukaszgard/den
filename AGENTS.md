# den — Developer Environment Navigator

Guidance for AI coding agents (and people) working in this repository. `CLAUDE.md`
imports this file.

## Project overview

`den` is a CLI with a Bubble Tea TUI that reaches private cloud data stores. It opens
tunnels (AWS SSM through a bastion by default, or SSH, EC2 Instance Connect, kubectl),
mints and refreshes the IAM credentials those stores accept, and wraps the daily
chores around them: AWS SSO logins, a FortiGate VPN, secrets, runbooks. It also serves
itself to AI agents over MCP (`den mcp`).

Supported stores: RDS/Aurora (PostgreSQL, MySQL), ElastiCache/Valkey, MemoryDB,
DocumentDB, Redshift (provisioned + Serverless), OpenSearch (domains + Serverless),
Neptune, Docker Compose. **Kafka is planned, not implemented**: connecting a `kafka`
service reports that on purpose.

## Tech stack

- Go (toolchain pinned in `go.mod`), module path `github.com/lukaszgard/den`
- Cobra (CLI), Bubble Tea + Bubbles + Lip Gloss + Glamour + Huh (TUI), all v2, imported as
  `charm.land/{bubbletea,bubbles,lipgloss,huh,glamour,log}/v2` (not `github.com/charmbracelet/...`)
- AWS SDK v2 and the `aws` CLI (SSM sessions, token minting, S3 updates)
- MCP: `github.com/modelcontextprotocol/go-sdk`; Claude: `github.com/anthropics/anthropic-sdk-go`
- YAML config (`gopkg.in/yaml.v3`), JSON Schema via `github.com/invopop/jsonschema`
- GoReleaser v2 for releases

## Build, run, check

```bash
go run ./cmd/den -c den.yaml     # the package path — cmd/den spans several files
make install                     # builds into ~/.local/bin/den
make check-fast                  # build + vet + tests without the slow lifecycle ones
make release-next                # the version this branch's commits call for (BASE=origin/main)
make check                       # what CI runs: build, vet, lint, race tests, govulncheck
make cover                       # race tests + per-package coverage floors
make generate                    # regenerate the den.yaml schema, docs/config, docs/cli and docs/doctor/install.md
make site                        # preview the documentation site (needs hugo >= 0.158)
```

Install without a checkout with `go install github.com/lukaszgard/den/cmd/den@latest`, or from
a checkout with `go install ./cmd/den`.

Commands: `den` (TUI), `den list|config|run --list [--json]`, `den doctor [--for F,…]
[--os OS] [--deep] [--json] [--explain]` (alias `den check-install`),
`den init --profile P`, `den paths [--json]`, `den schema`, `den mcp`,
`den run NAME`, `den runbook new|migrate|delete NAME`, `den runbook validate [FOLDER]`,
`den runbook source add|list|update|sync|remove`, `den runbook browse|install|uninstall|fork`,
`den vpn install-helper|status`, `den connect NAME`.
Config is discovered as `-c` (must exist) → `./den.yaml` → `platform.ConfigDir()/den.yaml`
(`~/.config/den`, or `$XDG_CONFIG_HOME/den`, `%APPDATA%\den` on Windows) → `~/.config/den/den.yaml`.
A person's own files (den.yaml, `vpn/*.conf`, runbooks) belong in `ConfigDir`; docs/files/README.md.

## Project structure

```
cmd/den/            CLI: main.go (root, TUI launch), services.go (service registry table,
                    config → Manager entries), doctorcmd.go, initcmd.go, mcpcmd.go,
                    runbookcmd.go, vpncmd.go, output.go (--json)
embed.go            package den: everything go:embed'ed (CHANGELOG.md, docs/help/*.md, schema)
internal/
  config/           den.yaml structs (schema.go), loading, validation, AWS config parsing
  platform/         ExpandHome, ConfigDir/StateDir/DataDir, OpenURL, ShellCommand (sh -c /
                    cmd /C), Detect (OS, distro, package managers)
  prereq/           catalog of the tools den runs: finding them, versions, per-OS install
                    steps, `--for` features; doctor and docs/doctor/install.md use it
  process/          Supervisor: runs external processes, logs, process-group stops
  service/          Service interface + capability interfaces, Manager (Reconcile), Summary
    ssmfwd/         shared tunnel lifecycle (Tunnel) + Engine + Transport (ssm/ssh/eice/kubectl)
    rds, redis, documentdb, redshift, opensearch, neptune, sigv4proxy, pgpass, awsauth
    tunnel/         user-defined SSH/SOCKS tunnels; vpn/ openfortivpn + privilege helper
    aws/            SSO session login; docker/ compose; kafka/ planned placeholder
  secrets/          Item interface; sops, awssm, keepassxc, vault; providers/ builds items
  runbook/          folders (runbook.yaml) and header scripts, rescan, execution, sequences,
                    params, redaction, run history on disk, scaffolding
  update/           release check, download, checksum verification, install
  doctor/           den doctor checks; discover/ den init; mcpserver/ den mcp; ai/ --explain
  agent/            starts an agent CLI (Claude Code, Codex, OpenCode, Copilot, Qwen, Droid, Pi,
                    Kimi) to write a runbook (den runbook new --agent)
  tui/              root model (app.go), panes.go (pane adapters), actions.go (service keys)
    screens/        one Bubble Tea model per pane; components/ shared widgets
  tools/schemagen/  generates schema/den.schema.json; tools/configdoc/ docs/config/README.md
                    (from the schema); tools/installdoc/ docs/doctor/install.md. docs/cli/README.md
                    comes from the command tree (cmd/den/clidoc_test.go, `-update`)
docs/               help/ (the in-app Help: one topic per feature) + one directory per feature
site/               documentation site (Hugo + Hextra): its pages are README.md, docs/*/README.md
                    and CHANGELOG.md, read in place; data/sections.yaml orders them
skills/den/         Agent Skill shipped to users; .claude/skills/ holds this repo's skills
```

## Architecture notes

- **Services.** Everything a user connects is a `service.Service`. Optional behaviour is
  discovered through capability interfaces — `TokenHolder`, `SecretCommander`,
  `Reconnectable`, `Monitorable`, `HealthCheckable`, `Describer` — never type names.
- **Tunnels.** Anything reached through a forward is an `ssmfwd.Engine` (where to forward,
  what credential to mint, how the user connects). The shared `ssmfwd.Tunnel` owns the
  process, readiness (the port accepting connections), retries, credential refresh,
  liveness monitor and reconnect; a `Transport` decides the command. Never copy that
  lifecycle into a service package.
- **Manager.** One `service.Manager` lives for the session. `Reconcile` swaps in the
  services a config describes and keeps unchanged running instances (config reload
  keeps connections). Exactly one goroutine forwards each service's events.
- **TUI.** `AppModel` routes through the `pane` interface (`internal/tui/panes.go`): one
  adapter per screen declares its size handling, h/esc ownership, help bar and service
  keys; `actions.go` carries out service keys. Service events arrive through a blocking
  waiter, not a poll; the redraw tick slows to 1 s when nothing moves.
- **Credentials never leave den** through `den mcp` or `--json`: `service.Summary` has no
  token field, MCP tools report how a client authenticates, logs are redacted.

## Conventions

- Match the surrounding code: comments explain *why* (the failure a line prevents), not what.
- New service type → `internal/service/<type>/` + a `config` spec struct and validator +
  an entry in `serviceFactories` (`cmd/den/services.go`) + its client in the `prereq`
  catalog and `Features` + docs. Skill: `add-service-type`.
- A tool den runs is described once, in `internal/prereq/catalog.go`: install recipes
  per platform (checked against the vendor's docs, never guessed package IDs), version
  pattern, minimum version with the reason. den prints install commands; it never runs
  a package manager.
- New transport → `internal/service/ssmfwd/transport.go` + `config.TransportSpec` +
  validation + doctor needs. Skill: `add-transport`.
- New TUI pane → screen model + adapter in `panes.go` + `paneMetadata`. Skill: `new-pane`.
- Bubble Tea v2 in the TUI: keys are `tea.KeyPressMsg` (read `msg.String()`, or `msg.Code`,
  `msg.Text`, `msg.Mod`; space is `"space"`), a paste is `tea.PasteMsg`. Only the root
  `AppModel.View()` returns `tea.View` (it sets `AltScreen`); pane and component `View()`
  stay plain strings that the root composes.
- The palette comes from the terminal's `tea.BackgroundColorMsg`, handled by
  `AppModel.setDark`, which hands it to every pane implementing the optional `darkAware`
  interface (`internal/tui/panes.go`). The TUI makes no probe before the program starts: a
  pane or widget whose colours depend on it implements `setDark`, it never asks the
  terminal. (The standalone huh forms outside Bubble Tea, `cmd/den/formtheme.go`, do one
  probe of their own.)
- Any line that names keys — a pane's hint line, the bottom bar, a view's help line, a
  pop-up's footer, a y/n question — is built with `components.Hints` or
  `components.HintLine`: `key: action`, the key in blue. Never write one by hand:
  `internal/tui/hints_rule_test.go` fails on a string that looks like one.
- Config change → structs in `internal/config/schema.go`, validation, the annotated
  `den.yaml`, an example in the feature's Help topic (`docs/help/<topic>.md`) and `make
  generate` (CI fails on a stale schema, `docs/config`, `docs/cli` or install reference).
  A key's description is the Go comment on its field: write one, since it becomes the
  editor hover text and the row of the configuration reference.
- `den.yaml` in the repo is the annotated example and ships in release archives; it must
  keep loading (`TestRepoConfigLoadsAndValidates`).
- Anything `go:embed`ed lives in the root package `den` (`embed.go`): embed cannot reach
  above its own directory.
- `docs/help/` is the in-app Help: `README.md` is the topic list (`## Group`, then
  `- [Title](file.md)`), every other file one topic in one template — `# Title`, a
  one-line summary, then `## What it is`, `What you can do`, `Before you start`,
  `Configure` (optional; keys as bullets, then several ```yaml examples), `Usage`
  (optional; step by step, numbered), `Limitations`, `When it goes wrong` (optional),
  `See also`. The sections are data in `internal/tui/screens/help/style.go`
  (`templateSections`, each with its icon and colour), and so are the list's groups
  (`groupLooks`): a new section or group needs an entry there. `internal/tui/screens/help/topics_test.go`
  holds every topic to it (no tables, links or images; one source line per paragraph or
  bullet, which glamour needs). `internal/config/helpdoc_test.go` decodes every snippet
  strictly, merges them into one config that must validate (so names stay unique across
  topics), and fails when a section, service type, transport or secrets provider has
  none. Keys are not listed there: a pane's keys go into its shortcuts pop-up
  (`Shortcuts()`).
- The runbook format is taught to agents by `skills/den/runbooks.md` (embedded, served by
  `den mcp`'s `runbook_guide`): a change to `runbook.yaml` keys or to what den sets in a
  run updates it too.

## Testing

Tests are expected for logic whose regressions would be invisible:

- Lifecycles run real processes against fakes on `PATH` (`fakeAWS`/`fakeBinary` in
  `ssmfwd`, fake `sops`/`vault`/`keepassxc-cli`, fake `aws` in `update`).
- Tests build key messages with `internal/tui/testkeys` (`Press`, `Text`, `Paste`), not by
  hand: the shape of a key message then lives in one file. A `tea.KeyPressMsg` literal is
  for what `testkeys.Press` cannot express, such as the modifier combinations a
  keyboard-enhancing terminal reports (`termview_test.go`).
- A feature that depends on the terminal (key disambiguation, the cursor, the title, the
  progress bar, OSC 52) is tested on `tea.View` and on the messages the terminal sends
  (`tea.KeyboardEnhancementsMsg`), both with and without support; a copy swaps
  `components.WriteClipboard`. A program under test can take `tea.WithWindowSize`
  and `tea.WithColorProfile` so its output does not depend on the machine.
- The TUI is tested by driving `AppModel.Update` with key messages over fake services
  (`internal/tui/app_test.go`); pin current behaviour there before refactoring routing.
- The CLI runs in-process through `newRootCmd()` against a temp config
  (`cmd/den/cli_test.go`) — never the developer's real `~/.aws`.
- MCP guardrails are tested through a real MCP client over in-memory transports.
- Slow lifecycle tests skip under `-short`.
- Packages that start goroutines have `func TestMain(m *testing.M) { leakcheck.Main(m) }`
  (`internal/leakcheck`): a goroutine the tests leave blocked for good fails the
  package. Tests release what they start — a harness ends with `shutdown()`, a
  `service.Manager` with `Close()`.
- TUI redraw cost has benchmarks (`internal/tui/bench_test.go`); compare with
  `benchstat` before and after a change to rendering.
- `scripts/coverfloor.sh` enforces per-package coverage floors in CI; raise a floor
  when adding tests, never lower one to pass.

Run `make check` before committing. UI changes also need a manual run of `den`.

## Commits

`<type>: <summary>` (type: feat, fix, refactor, perf, test, docs, build, ci; `!` after the type marks a breaking change),
imperative summary, body explaining what changed and why.

When a branch's work is finished — committed, `make check` passing — push it and open
its PR into `main` with `.github/pull_request_template.md` filled in. Skill: `open-pr`.

## Feature documentation

Every new feature or significant update **must** include:

1. **`docs/<feature>/README.md`** — *What it is*, *Configuration* (the `den.yaml` snippet
   with every key explained), *Prerequisites* (tools, IAM permissions, env vars), *Usage*.
   This is the long reference; GIFs, tables and links belong here. It is also a page of
   the documentation site: list it in a group of `site/data/sections.yaml` (the site
   build fails until it is) and link other docs relatively, as GitHub reads them.
2. **`docs/help/<feature>.md`** — its in-app Help topic, in the template above, listed in
   `docs/help/README.md`: short, with several configuration examples.
3. **`docs/<feature>/<feature>.tape` and `<feature>-hd.tape`** — a
   [VHS](https://github.com/charmbracelet/vhs) recording of this feature only, rendered
   twice from one script, `<feature>-steps.tape`. The steps hold the scenes, and any `Env`
   or `Set WaitTimeout` they need; never an `Output`, a `Source` or a `Set` of the look.
   Every recording has all three files, new or old:

```
# <feature>.tape: the README's GIF, 1200x800
Output docs/<feature>/<feature>.gif

Source docs/tape-settings.tape
Source docs/<feature>/<feature>-steps.tape
```

```
# <feature>-hd.tape: the site's video, 2400x1600
Output docs/<feature>/<feature>.mp4

Source docs/tape-settings-hd.tape
Source docs/<feature>/<feature>-steps.tape
```

The site plays `<feature>.mp4` in place of the GIF next to it, and a 1200 px video looks
soft on a retina screen, so the video is rendered at twice the size with the font
doubled. The GIF stays at 1200x800: GitHub shows only the GIF, and one twice the size
weighs several times as much. The steps are a file of their own because VHS does not
nest `Source`; sharing them keeps the GIF and the video from drifting, as `main.tape`,
`main-hd.tape` and `main-steps.tape` do for the home recording.

`docs/tape-settings.tape` holds the window, the font and the typing speed, and
`docs/tape-settings-hd.tape` the same at twice the size, so a size change is one edit in
each. Both give the same columns and rows, so the TUI lays out alike in the GIF and the
video. `tapes_test.go` fails on a tape under `docs/` laid out any other way, and on HD
settings that are not twice the base ones. Tapes run from the repo root, where `Source`
and `Output` paths resolve (`vhs secrets.tape` inside `docs/secrets` fails to find the
settings): `make tape TAPE=secrets` renders the GIF and its `-hd` video; `make demo`
skips the `-steps` files.

Pace it for a person watching. A viewer needs about 0.5 s to find what changed after a
cut, then reads about 3 words a second, and the site video loops muted, so they may join
mid-scene. Each hold names its tier in a trailing comment (`Sleep 2.5s   # Arrive`):

- **Transit** 700 ms: a key on the way somewhere, not meant to be read; at most 2 in a row.
- **Arrive** 2.5 s: a new panel appears on the right, the menu still has focus.
- **Look** 3.5 s: a panel gets focus, or `enter` opens a view.
- **Read** 2 s + 0.5 s per line meant to be read, 4–8 s: the thing the scene is about.
- **Act** 1 s: before a key that changes the screen, so the eye finds the cursor.
- **Type**: queries at the shared 120 ms, then 1.5 s; a long command or prompt as
  `Type@40ms "…"`, then Act before `Enter`.
- **Back** 1 s: `esc`/`tab` back to a screen already seen.
- **Scroll**: a key repeated every 200 ms through a long list (`Down@200ms 9`), then Look.
- **Wait**: anything slow (a connect, a login, a run, a CLI command) is
  `Wait+Screen@<timeout> /regex/` (a TUI string grepped from the code) or `Wait@<timeout>`
  (the shell prompt, after a CLI command), then Read. Never a blind long `Sleep`.

Start: a TUI tape launches den hidden and `Wait+Screen@20s /<text of the first pane>/`
before `Show`, then holds 2 s (`/Connect/` for the repo `den.yaml`, the first `menu_order`
pane's title for a tape's own config). End: the last Read, then `Hide`, `Type "q"`,
`Sleep 500ms`; a CLI tape ends on its last Read. One idea per scene, with a comment saying
what to notice. Length: home video 35–60 s, a feature tape at most 60 s visible.
`vhs validate <tape>` checks a tape; never run `vhs <tape>` outside `make demo` or `make tape`.

Tapes that need AWS are written but rendered by a person (`make demo`, or `make tape` for one): they drive
the real app against real accounts. The skill `feature-docs` has the checklist.

## Releases

Pushing a `v*.*.*` tag runs `.github/workflows/release.yaml` (GoReleaser: macOS/Linux/
Windows × amd64/arm64, `checksums.txt`, `latest.json`, `install.sh` and `install.ps1` as release assets). The in-app updater
verifies downloads against `checksums.txt`. `make release-draft`, `release-prep` and
`release-tag` (`scripts/release.sh`, all take `VERSION=X.Y.Z`) script the steps around the
changelog entry and the release PR, which stay manual. Skill: `release`.

**Versioning.** The version follows the commit types since the base ref, not a fixed
patch bump:

| Commits since the base | 0.x | ≥ 1.x |
|---|---|---|
| `<type>!:` or a `BREAKING CHANGE:` line in a body | minor | major |
| any `feat` | minor | minor |
| any `fix` or `perf` | patch | patch |
| only `docs`, `test`, `refactor`, `build`, `ci` | no release | no release |

The base version is the higher of the newest `v*` tag and the top `## [X.Y.Z]` of
`CHANGELOG.md` on the base ref (an open release PR's entry counts once merged). `make
release-next` prints it: `last vX (…)`, `bump minor — N feat, N fix, N breaking`,
`next vX.Y.Z`. A breaking change on 0.x gets a `**Breaking:**` line at the top of the
entry with the migration; 1.0.0 is a separate, later decision. CI's `release-version` job
only warns when the entry is lower than expected; it never fails the build.
