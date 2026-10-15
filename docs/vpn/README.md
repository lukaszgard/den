# VPN

## What it is

The VPN feature wraps [openfortivpn](https://github.com/adrienverge/openfortivpn) as a first-class `den` service, so you can bring the corporate FortiGate VPN up and down from the TUI with live logs and automatic SAML browser handoff. No more copy-pasting a sudo command and hunting for the login URL in your terminal scrollback.

## Configuration (`den.yaml`)

VPNs live in a top-level `vpn:` list, alongside `tunnels:`. They are not regular services — no region, AWS profile, or EC2 instance is needed.

**A gateway is enough.** The smallest entry is a name and a gateway; den passes
`--saml-login` and fills in port 443 and the SAML listener (8020) itself:

```yaml
vpn:
  - name: "Office VPN"
    gateway: vpn.example.com
```

Everything else is optional:

```yaml
vpn:
  - name: "Office VPN"
    description: "Corporate VPN (openfortivpn + SAML)"
    gateway: vpn.example.com
    extra_args: []                         # optional, extra flags passed to openfortivpn
    health_check: "intranet.example.com:443" # optional, internal host:port probed to verify the data path
    config_file: ~/.config/den/vpn/office.conf # optional, only for settings den has no key for
```

| Field          | Required | Description                                                                                          |
|----------------|----------|------------------------------------------------------------------------------------------------------|
| `name`         | yes      | Display name shown in the TUI                                                                        |
| `gateway`      | yes, unless `config_file` sets `host` | FortiGate gateway host (e.g. `vpn.example.com`), `host` or `host:port`           |
| `description`  | no       | Free-form subtitle shown next to the name                                                            |
| `extra_args`   | no       | Extra arguments appended after `--saml-login` (e.g. `--trusted-cert ...`)                            |
| `health_check` | no       | Internal `host:port` reachable **only** via the VPN. den TCP-probes it to confirm the data path is alive (see below). |
| `config_file`  | no       | Path to an openfortivpn `key = value` file (`-c`), for settings den has no key for: `set-dns`, `trusted-cert`, `persistent`. A relative path is read against den.yaml's folder |
| `reconnect`    | no       | Re-establish a tunnel that dropped after being fully connected — three attempts (30 s, 60 s, 120 s). Needs the privilege helper; see [Auto-reconnect](#auto-reconnect) |

### Where the settings file goes

Write `config_file` only when a gateway is not enough. It is an openfortivpn configuration
in INI form, so name it `.conf`, not `.yaml`, and keep it in your config directory, not in
a repository: `~/.config/den/vpn/office.conf`. It can hold `password =`, so make it
readable by you alone (`chmod 600`). `den doctor --for vpn` warns when it is readable by
others or sits inside a git work tree, and `den vpn status` lists each entry with its file
and whether it exists. See [Files and folders](../files/README.md).

## Leaked sessions

openfortivpn runs detached as root, so a session den loses track of is reparented
to init and survives den restarts. It stays invisible locally — no den entry
mentions it — but the gateway still counts it against your account's
concurrent-session limit. Once enough accumulate, FortiGate stops allowing new
logins and the SAML page returns:

```
Forbidden
You don't have permission to access /remote/saml/start on this server.
```

den now tears the process down whenever a tunnel drops, times out mid-login, or
loses its interface, and `den vpn install-helper`'s stop sweeps for strays rather
than only killing the pid it recorded. openfortivpn logs out of the gateway when
it is told to stop, and over a dead connection that logout never returns, so the
stop gives it 5 seconds and then kills it outright. When one survives even that,
the helper's stop fails and names its pid in the VPN log. To check and clear by
hand:

```bash
pgrep -x openfortivpn           # more than one pid means strays
ifconfig | grep '^ppp'          # more than one ppp interface means stale pppd
sudo pkill -x openfortivpn      # clears them all, including your live tunnel
sudo pkill -x pppd              # ...and the pppd children they leave behind
```

Note `pgrep -al` does not work on macOS — there is no `-a` flag, and the failure
is easy to misread as "nothing running". Use `pgrep -x` and check the exit code.

Killing openfortivpn is not enough on its own: it spawns `pppd`, and a killed
parent leaves that child reparented to init with its `ppp` interface still up,
still holding a VPN-assigned address. den's stop now reaps those, matching on
openfortivpn's fixed ppp peer address plus a parent of init so a live tunnel's
pppd is never touched. pppd can ignore TERM too: it answers with an LCP
terminate over a link that is gone and may never exit, keeping `ppp0` and the
VPN's split DNS. So the reap also waits 5 seconds and then kills what is left,
and the helper's stop fails with `pppd (pid N) did not stop` when one survives
even that. Note that macOS hides root processes' command lines from non-root
callers: `pgrep -f` as you sees nothing, so the sweep finds them because it runs
as root, and den finds them with `ps`, which is setuid root there.

### Something left running

den does not start a second openfortivpn beside one that is still running, nor
beside an orphaned pppd. Either holds `ppp0` and the DNS, so a new tunnel could
only fail: its pppd would get `ppp1` while den watches `ppp0`, and its SAML login
would hold another gateway session meanwhile. Press `c`, and den lists what is
left instead of connecting:

```
╭────────────────────────────────────────────────────────────────────────╮
│  Office VPN: a VPN from an earlier session is still running            │
│  A new connect cannot run beside it: it holds ppp0 and the VPN's DNS.  │
│                                                                        │
│    pppd  pid 29035    running 3 days                                   │
│                                                                        │
│  To stop it yourself:                                                  │
│    sudo /opt/den/libexec/den-vpn stop office-vpn                       │
│    sudo kill 29035; sleep 5; sudo kill -9 29035 2>/dev/null            │
│                                                                        │
│  y: stop it and connect  s: copy stop  k: copy kill  n: cancel         │
╰────────────────────────────────────────────────────────────────────────╯
```

| Key           | Action                                                                 |
|---------------|------------------------------------------------------------------------|
| `y`           | Run den's stop, then connect once nothing is left                      |
| `s`           | Copy den's stop as you would run it (only with the helper installed)   |
| `k`           | Copy the kill by pid: TERM, then KILL after 5 seconds                  |
| `n` / `esc`   | Close it and connect nothing                                           |

- The helper's stop (`sudo /opt/den/libexec/den-vpn stop <profile>`) is shown
  only once [`den vpn install-helper`](#passwordless-setup) has run: its sudoers
  rule is what lets it run as root. Without the helper, `y` raises the admin
  password dialog, and the pop-up says so.
- When den's stop leaves something running, the pop-up opens again, titled
  `den's stop left this running`, and `y` tries again. A `d` whose stop left
  something running opens the same pop-up.
- It never opens on its own at startup: without the helper its stop raises an
  admin dialog, which must not appear unasked.
- While another of den's VPNs is up, `c` asks you to disconnect it first rather
  than offering to kill its tunnel.
- The log keeps the kill command after the pop-up has gone:
  `not starting openfortivpn: pppd (pid 29035) is still running — to stop it yourself: sudo kill …`.

The stop logic lives in the installed script, so a helper installed by an older
den keeps its older stop. `den vpn status` shows `script: outdated` and `den
doctor` warns when the installed script is not the one this den would install;
re-run `den vpn install-helper` then.

## Prerequisites

- **openfortivpn 1.23.0 or newer** in `$PATH`. den logs in with SAML (`--saml-login`, and
  `saml-login` in the helper's config), which openfortivpn added in 1.23.0. `den doctor
  --for vpn` checks the version and prints the fix for your system.
  - macOS: `brew install openfortivpn`
  - Ubuntu 25.10, Debian 13 and newer: `sudo apt install openfortivpn`
  - Fedora 43 and newer, RHEL 10 (EPEL): `sudo dnf install openfortivpn`
  - **Ubuntu 24.04 and older, Debian 12 and older, RHEL 9 (EPEL)**: the distribution's
    package predates SAML, and apt or dnf have nothing newer for these releases:

    | Release | Its openfortivpn |
    |---|---|
    | Ubuntu 22.04 | 1.17.1 |
    | Ubuntu 24.04 | 1.21.0 |
    | Debian 12 | 1.19.0 |
    | RHEL 9 (EPEL) | 1.21.0 |

    Build it from source instead ([upstream's steps](https://github.com/adrienverge/openfortivpn#building-and-installing-from-source)).
    These were run on Ubuntu 22.04, Ubuntu 24.04 and Rocky Linux 9:

    ```sh
    sudo apt-get remove -y openfortivpn
    sudo apt-get install -y git gcc automake autoconf libssl-dev make pkg-config ppp
    # RHEL: sudo dnf remove -y openfortivpn
    #       sudo dnf install -y git gcc automake autoconf openssl-devel make pkg-config ppp
    src=$(mktemp -d) && git clone --depth 1 --branch v1.24.1 https://github.com/adrienverge/openfortivpn.git "$src"
    (cd "$src" && ./autogen.sh && ./configure --prefix=/usr/local --sysconfdir=/etc --enable-legacy-pppd && make && sudo make install)
    openfortivpn --version        # 1.24.1, from /usr/local/bin
    den vpn install-helper        # again: the helper stores openfortivpn's path
    ```

    `--enable-legacy-pppd` is for pppd older than 2.5.0, which all of these releases ship:
    without it openfortivpn passes pppd `ipcp-accept-remote`, which those reject. Leave it
    out when building for pppd 2.5 or newer.
- **A kernel with PPP (Linux)**: pppd builds the tunnel on the kernel's PPP driver. Desktop
  distributions have it; WSL 3.0.1 does not. See [Linux kernel and WSL](#linux-kernel-and-wsl).
- **Privilege escalation** — openfortivpn needs root (it drives `pppd`, routes and DNS). `den` never asks for your password inside the TUI. Either:
  - **The privilege helper** (recommended) — one-time setup, after which connect and disconnect never prompt. See [Passwordless setup](#passwordless-setup).
  - **The OS's native admin prompt** — the fallback when the helper is not installed:
    - **macOS** — `osascript` + `do shell script … with administrator privileges` (Keychain dialog)
    - **Linux** — `pkexec` from PolicyKit (`apt install policykit-1` / `dnf install polkit`)
    - **Windows** — not supported; the service is skipped with a warning in `den`'s startup log. Use FortiClient instead.
- **Default browser** — required for SAML login (`open` on macOS, `xdg-open` on Linux).

### Linux kernel and WSL

openfortivpn hands the tunnel to pppd, and pppd needs the kernel's PPP driver
(`ppp_generic`, `CONFIG_PPP`). Without it the SAML login still works, and the tunnel then
fails on `/dev/ppp`. `den doctor` checks this on Linux whenever a `vpn:` entry exists (and
for `den doctor --for vpn`). It reads `/proc/devices` and `/lib/modules/$(uname -r)`.

| Kernel | PPP | What to do |
|---|---|---|
| Ubuntu, Debian, Fedora desktop kernels | module `ppp_generic`, loaded on first use | nothing; `sudo modprobe ppp_generic` if it does not load |
| WSL 2.7.14 and older (kernel 6.18.33.2, 6.6.x) | built in | nothing |
| **WSL 3.0.1** (kernel 6.18.40.1-microsoft-standard-WSL2) | **none**: `# CONFIG_PPP is not set` | roll WSL back, or use FortiClient on Windows |
| any kernel whose `/lib/modules/$(uname -r)` is gone | module missing | reboot into the installed kernel |

`uname -r` tells you which kernel runs: WSL kernels end in `microsoft-standard-WSL2`.

WSL 3.0.1 dropped PPP from its kernel; Microsoft tracks it as
[microsoft/WSL#41750](https://github.com/microsoft/WSL/issues/41750), with no fix yet. The 3.0.2
pre-release notes do not mention it. To get PPP back in WSL, install
`Microsoft.WSL_2.7.14.0_x64_ARM64.msixbundle` from the
[2.7.14 release](https://github.com/microsoft/WSL/releases/tag/2.7.14), then `wsl --shutdown`
and check `uname -r` in the distribution reads `6.18.33.2-microsoft-standard-WSL2`. A later
`wsl --update` brings 3.0.x and its kernel back. Otherwise, connect with FortiClient on
Windows: WSL's traffic is routed through Windows, so it normally uses the host's VPN.

## Usage

1. Run `den`.
2. Select the VPN service in the Connect panel and press `c`.
3. With the [privilege helper](#passwordless-setup) installed, nothing is asked — skip to 4. Otherwise the native admin password dialog appears; approve it.
4. openfortivpn starts, prints `Authenticate at 'https://…'`, and `den` auto-opens the URL in your default browser.
5. Complete the SAML login in the browser.
6. When `Tunnel is up and running` appears in the logs, the status dot turns green. `ifconfig ppp0` confirms the interface.
7. Press `d` to disconnect. Without the helper a second admin prompt appears (expected — `osascript` does not cache credentials), then the tunnel is torn down.

### Keys

| Key       | Action                                  |
|-----------|-----------------------------------------|
| `c`       | Connect the selected VPN; with a VPN [left running](#something-left-running), offer to stop it first |
| `d`       | Disconnect the selected VPN             |
| `r`       | Toggle `reconnect`                      |
| `H`       | Toggle the `health_check` probe         |
| `Enter`   | Open detail view                        |
| `h` / `?` | [Shortcuts pop-up](../shortcuts/README.md) |
| `↑` / `k` | Move cursor up                          |
| `↓` / `j` | Move cursor down                        |

`r` and `H` also work in the detail view. Lowercase `h` opens the shortcuts pop-up,
here and in the detail view, which is why the health-check toggle is the uppercase `H`.

### Runtime toggles

The status box shows one dot per setting, read from the service itself, so it
tracks the current value whether the VPN is up or down:

```
● Reconnect
○ Health  intranet.example.com:443 (off)
```

Green means on, grey means off, and a dimmed dot with `(not configured)` means
there is nothing to toggle — pressing `H` on a VPN with no `health_check` target
does nothing but say so.

Toggles are **runtime-only**: they last until `den` exits and are never written
back to `den.yaml`. There is no separate `monitor` toggle as there is for tunnels,
because for a VPN the health check *is* the monitor — one loop, one switch.

## Auto-reconnect

With `reconnect: true`, a tunnel that drops after being fully connected is
re-established automatically: **30 s, then 60 s, then 120 s**, after which den
gives up and waits for you to press `c`.

This is deliberately far more conservative than the equivalent for SSH tunnels,
which retries indefinitely on a 5 s → 60 s backoff. A VPN connect is not silent:

- **Every attempt re-runs the SAML login**, which opens a browser tab. That is
  why the attempt count is capped rather than open-ended.
- **Auto-reconnect requires the [privilege helper](#passwordless-setup).** Without
  it openfortivpn needs root, and each retry would raise an OS password dialog
  with nobody there to answer it. den logs
  `not reconnecting: needs the passwordless VPN helper` and stops instead.
- **A login that times out is never retried.** A half-finished SAML login holds a
  session slot on the gateway, and enough of those make `/remote/saml/start`
  answer 403 to *every* new login — see [Leaked sessions](#leaked-sessions).
  den logs `not reconnecting: the SAML login never completed` and stops.
- Each retry tears the old session down first, for the same reason.

While waiting, the pane shows `RECONNECTING (Ns)` counting down. Turning `r` off
during that wait stops any further attempts, but does not cancel the one already
scheduled.

The first lines in the log pane say which mode is in use:

```
privilege mode: passwordless helper (/opt/den/libexec/den-vpn) — no admin prompt
privilege mode: admin prompt — helper is not installed — run: den vpn install-helper
```

If the SAML login is not completed within 2 minutes, the service transitions to `error` and stops cleanly.

## Passwordless setup

`den vpn install-helper` installs, as root, a small wrapper plus a sudoers rule pinned
to it. After that den starts and stops the VPN with `sudo -n`, so **neither connect nor
disconnect asks for anything**.

```bash
den vpn install-helper      # prints everything it will write, then asks for your password once
den vpn status              # usable / not usable, script current / outdated, per-profile state
den vpn uninstall-helper    # back to the admin prompt
```

Re-run `install-helper` after changing a `vpn:` entry in `den.yaml`: the helper runs the
copy of those settings made at install time, not your config file.

### What it installs

| Path | Owner / mode | Purpose |
|---|---|---|
| `/opt/den/libexec/den-vpn` | root:wheel 0755 | the wrapper |
| `/opt/den/etc/<profile>.conf` | root:wheel 0600 | openfortivpn settings rendered from your `vpn:` entry |
| `/opt/den/var/log/<profile>.log` | yours, 0600 | openfortivpn output, which den tails for the SAML URL |
| `/opt/den/var/run/<profile>.pid` | root:wheel 0644 | pid of the detached openfortivpn |
| `/etc/sudoers.d/den-vpn` | root:wheel 0440 | the NOPASSWD rules |

The sudoers rules pin whole commands — the wrapper alone is never granted:

```
alice ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn check
alice ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn start office-vpn
alice ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn stop office-vpn
alice ALL=(root) NOPASSWD: /opt/den/libexec/den-vpn status office-vpn
```

**The wrapper takes a profile name and nothing else.** That is the entire point of it.
A rule like `NOPASSWD: openfortivpn` would let anything on your machine run
`openfortivpn --pppd-plugin=/tmp/evil.so` (loads a shared library as root) or
`--pppd-log=/etc/sudoers` (writes to any file as root). Instead every parameter comes
from the root-owned config written at install time, and `install-helper` refuses to
bake in `pppd-plugin`, `pppd-call` or `pppd-log` even if your config file sets them.

Everything lives under `/opt`, which is root-owned. `/Library` and `/usr/local` are
writable by admin users on macOS, so a wrapper there could be swapped for another script
that sudo would then run as root. den re-checks this on every connect: if the wrapper or
any directory above it is not root-owned, or is group/world-writable, den refuses to use
it and falls back to the admin prompt, saying why in the log pane.

### What you are trading away

After this, **anything running as your user can become root without a password.** Not
through den's wrapper — through the binary it ends up running.
`/opt/homebrew/bin/openfortivpn` and the OpenSSL libraries it links are writable by your
user (Homebrew installs them that way), so malware running as you could replace one and
wait for the next connect.

That is the honest cost of never being asked again. Ways to think about it:

- **Accept it** — a single-user laptop where you are already an admin, and anything
  running as you could phish the password prompt anyway.
- **Close the hole** — make the binary and its libraries root-owned, after which the
  rule grants nothing a local attacker can subvert. Homebrew will then need `sudo` for
  upgrades of these two formulae, and `den vpn install-helper` must be re-run if the
  path changes:
  ```bash
  sudo chown -R root:wheel "$(brew --prefix)/Cellar/openfortivpn" "$(brew --prefix)/Cellar/openssl@3"
  ```
- **Don't install it** — `den vpn uninstall-helper`, and keep approving two dialogs per
  session.

### Troubleshooting

- The SAML login works, then the log shows `Couldn't open the /dev/ppp device`,
  `/usr/sbin/pppd: Please load the ppp_generic kernel module` and `ERROR: pppd: The
  kernel does not support PPP`: the kernel has no PPP driver, which pppd needs for the
  tunnel. den stops openfortivpn there, shows the cause, and does not reconnect. See
  [Linux kernel and WSL](#linux-kernel-and-wsl); `den doctor --for vpn` names the fix for
  the machine. Creating `/dev/ppp` with `mknod` does not help: the node exists, but
  nothing in the kernel answers it (`No such device or address`).
- `ERROR: Could not authenticate to gateway` every few seconds after a tunnel failed: the
  openfortivpn config file sets `persistent = N`, so openfortivpn reruns the tunnel every
  N seconds with the SAML session the gateway closed at the failure. Only a new login
  works. den stops it on the errors it knows to be final (above); for anything else,
  disconnect with `d`, fix the first error in the log, and connect again.
- The log shows `WARN: Bad key in configuration file: "saml-login"`, then `VPN account
  password:` and `ERROR: Could not authenticate to gateway`: openfortivpn is older than
  1.23.0 and has no SAML. It stops reading the helper's config at that line, keeps the
  gateway it had already read, and falls back to a password login with an empty
  password. Upgrade openfortivpn (see [Prerequisites](#prerequisites)), then re-run
  `den vpn install-helper`. Without the helper, the same openfortivpn exits with
  `unrecognized option '--saml-login'`.
- `den vpn status` → `not usable — passwordless sudo rule is not active`: the wrapper is
  there but `/etc/sudoers.d/den-vpn` is missing or names a different user. Re-run
  `den vpn install-helper`.
- `not usable — … is not owned by root` / `is writable by group or others`: something
  changed the wrapper or a parent directory. Re-run `den vpn install-helper`; den will
  not use a wrapper anyone but root can rewrite.
- `unknown profile 'x' — re-run: den vpn install-helper`: a `vpn:` entry was renamed in
  `den.yaml` after the install.

## Verifying connectivity (split-tunnel)

A split-tunnel VPN (the usual FortiGate setup): only corporate subnets route through it, while
general internet traffic keeps going out your normal connection. This means
`curl ifconfig.me` returns your **real public IP even when the VPN is up** — so
it is *not* a valid way to test whether the VPN is connected. The right test is
whether an internal host that's reachable only over the VPN responds.

By default `den` infers "connected" from two local signals: a running
`openfortivpn` process and the presence of the `ppp0` interface. Neither proves
the tunnel actually carries traffic — after a tunnel dies, both can linger as
zombies and `den` would keep showing **connected**.

Set `health_check` to an internal `host:port` (something always-on and reachable
only over the VPN — e.g. an intranet site on `:443`, an internal DB host, a
bastion) and `den` will TCP-probe it:

- **On adopt** — at startup, before adopting a tunnel from a previous session. The
  probe runs in the background, so den opens at once and shows the VPN as
  **connecting** until `health_check` answers (up to three 2 s tries). If the
  process and `ppp0` are present but the probe fails, the VPN is shown as
  **error** ("data path appears dead") instead of a false *connected*. Press `c`:
  den offers to tear down the zombie and reconnect (see
  [Something left running](#something-left-running)). Tunnels bound to the VPN
  auto-connect only once the probe passes.
- **While connected** — every 15 s. Two consecutive failures flag the tunnel as
  dropped (→ error), even if `ppp0` is still present.

The configured target is shown under **Data path** in the VPN status box. When
`health_check` is unset, `den` falls back to interface-only detection (the
previous behavior).

## Session persistence

openfortivpn runs as a detached root process (reparented to `launchd` on macOS, launched via `pkexec` on Linux, or detached by the privilege helper), so **the tunnel keeps running after you quit `den`**. On the next launch `den` detects the live tunnel — it checks for both a running `openfortivpn` process and the `ppp0` interface (and, when `health_check` is set, that the internal host is reachable — see [Verifying connectivity](#verifying-connectivity-split-tunnel)) — and shows the VPN as **connected** straight away (or **connecting** while that check runs in the background), with no second admin prompt and no duplicate process. `den list` and `den mcp` show such a VPN as `connecting`: they do not wait for the check. The adopted tunnel is monitored for drops and can be disconnected from the dashboard with `d` exactly like one `den` started itself.

A tunnel that is still mid-SAML (process up but `ppp0` not yet established) is intentionally not adopted; finish or cancel the login and it resolves on the next launch.
