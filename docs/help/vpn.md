# VPN

FortiGate VPN with a browser login, no sudo

## What it is

den brings a FortiGate SSL VPN up and down through `openfortivpn`, logging in with SAML in your browser. It opens the login page for you, shows openfortivpn's log, and tells you when traffic really flows. Use it instead of a `sudo openfortivpn` in a terminal and a login URL fished out of its output.

## What you can do

- Connect and disconnect a VPN from the VPN panel or the Connect list; den opens the SAML login in your default browser.
- Never type a password: after `den vpn install-helper` once, den starts and stops the VPN with no prompt at all.
- Prove the data path works, not just the interface: `health_check` probes an internal host that only the VPN reaches.
- Reconnect a VPN that dropped, at most three times (after 30, 60 and 120 seconds), since every attempt opens a browser login.
- Start your SSH tunnels when the VPN comes up and stop them when it goes down (see Tunnels).
- Pick up a VPN left running by an earlier den session instead of starting a second one.
- Keys: press `h` in the VPN panel.

## Before you start

- `openfortivpn` 1.23.0 or newer, the first with SAML login (`den doctor --for vpn` checks the version): `brew install openfortivpn` on macOS, the `openfortivpn` package on Ubuntu 25.10, Debian 13, Fedora 43 and newer.
- Ubuntu 24.04 and older, Debian 12 and older and RHEL 9 (EPEL) ship an older openfortivpn (Ubuntu 22.04 has 1.17.1): build it from source as below; on RHEL use `dnf` and `openssl-devel` in place of `apt-get` and `libssl-dev`. Doctor prints the same steps for your system. The legacy pppd option is for the pppd 2.4 these releases ship.

```
sudo apt-get remove -y openfortivpn
sudo apt-get install -y git gcc automake autoconf libssl-dev make pkg-config ppp
src=$(mktemp -d) && git clone --depth 1 --branch v1.24.1 https://github.com/adrienverge/openfortivpn.git "$src"
(cd "$src" && ./autogen.sh && ./configure --prefix=/usr/local --sysconfdir=/etc --enable-legacy-pppd && make && sudo make install)
openfortivpn --version
den vpn install-helper
```

- On Linux, a kernel with PPP: pppd sets up the tunnel through it. Desktop distributions have it. WSL 3.0.1's kernel (6.18.40.1) has none: roll WSL back to 2.7.14, or use FortiClient on Windows. Doctor checks the kernel.
- A default browser for the SAML login.
- Root for openfortivpn: run `den vpn install-helper` once, and again after every change to a `vpn:` entry. Without it den asks for your password through the OS dialog on each connect and disconnect.

## Configure

A `vpn:` list. Each entry needs `gateway`, `config_file`, or both. A gateway is enough: den passes `--saml-login` and fills in port 443 itself, so write a `config_file` only for openfortivpn settings den has no key for.

- `name` (required): shown in den; tunnels name it in `auto_connect`.
- `gateway`: the FortiGate host, `host` or `host:port`.
- `config_file`: an openfortivpn `key = value` file, passed with `-c`, for settings such as `set-dns`, `trusted-cert` and `persistent`. Name it `.conf`, keep it in `~/.config/den/vpn/` and `chmod 600` it: it can hold a password.
- `description`: shown next to the name.
- `extra_args`: more openfortivpn arguments, such as `--trusted-cert`.
- `health_check`: an internal `host:port` that only the VPN reaches, probed to confirm the data path.
- `reconnect` (default `false`): reconnect after a drop, up to three times; it needs the helper.

The smallest entry: a gateway with a browser login.

```yaml
vpn:
  - name: office
    gateway: vpn.example.com
```

A VPN checked through an internal host and reconnected after a drop:

```yaml
vpn:
  - name: office (checked)
    description: office network, SAML login
    gateway: vpn.example.com
    health_check: intranet.example.com:443
    reconnect: true
```

Settings kept in an openfortivpn file, plus flags that leave your DNS alone:

```yaml
vpn:
  - name: lab vpn
    config_file: ~/.config/den/vpn/lab-vpn.conf
    extra_args: [--set-dns=0, --pppd-use-peerdns=0]
```

Then, once:

```
den vpn install-helper
den vpn status
```

## Limitations

- FortiGate SSL VPN with SAML only, through openfortivpn 1.23.0 or newer; other VPNs (Cisco, WireGuard, AWS Client VPN) are not supported.
- No Windows support: openfortivpn has no Windows build; use FortiClient there. In WSL it needs a WSL kernel with PPP, which 3.0.1 does not have.
- Never commit a `config_file`: `den doctor --for vpn` warns when it is readable by others or sits inside a git work tree.
- The helper runs a copy of your `vpn:` settings made when you installed it: change an entry and install it again.
- With the helper, anything running as your user can start openfortivpn as root; `docs/vpn/README.md` explains the trade-off and how to close it.
- A SAML login not finished within 2 minutes fails, and is never retried: half-finished logins use up the gateway's session slots.

## When it goes wrong

- `Bad key in configuration file: "saml-login"`, then `VPN account password:` and `Could not authenticate to gateway`: openfortivpn is older than 1.23.0 and has no SAML. It drops the helper's config at that line and tries a password nobody typed. Upgrade it (see Before you start), then run `den vpn install-helper` again.
- `unrecognized option '--saml-login'`: the same old openfortivpn, without the helper.
- The login works, then `Couldn't open the /dev/ppp device`, `Please load the ppp_generic kernel module` and `The kernel does not support PPP`: the kernel has no PPP. den stops openfortivpn and says so. On WSL 3.0.1, roll WSL back to 2.7.14; elsewhere `sudo modprobe ppp_generic`. Doctor names the fix for your machine. Creating `/dev/ppp` by hand does not help.
- `Could not authenticate to gateway` every few seconds after a failed tunnel: the config file sets `persistent`, so openfortivpn retries with a login the gateway already closed. Fix the first error, then connect again.
- The SAML page says `Forbidden … /remote/saml/start`: the gateway counts too many sessions for you, often stray openfortivpn processes. `sudo pkill -x openfortivpn` and `sudo pkill -x pppd` clear them, the live one included.
- `openfortivpn (pid N) is still running`, or `pppd (pid N)`: a VPN den could not take over or stop is still up, often an openfortivpn hung after a dropped tunnel or the pppd it left behind, and a second one would only cost another login. Press `c`: den lists what is left and offers to stop it, or shows the commands to do it yourself.
- `the tunnel data path appears dead`: openfortivpn and ppp0 from an earlier session are there, but the health_check host does not answer. Press `c`: den offers to stop them and connect again.
- `den's stop left this running`: the process ignored den's stop. Copy the kill command with `k` and run it, or reinstall an outdated helper and press `y` to try again.
- `den vpn status` shows `script: outdated`, or doctor warns about the VPN helper: the installed helper is older than den, and its stop cannot clear a hung openfortivpn or pppd. Run `den vpn install-helper` again.
- `not reconnecting: needs the passwordless VPN helper`: reconnect needs `den vpn install-helper`.
- `not reconnecting: the SAML login never completed`: a login timed out, and den does not retry those on purpose; connect again and finish the login in the browser.

## See also

- Tunnels
- Files and folders
- Doctor
- `docs/vpn/README.md`
