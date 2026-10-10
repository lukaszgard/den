# Security policy

## Reporting a vulnerability

Please report a vulnerability privately, through GitHub's
[private vulnerability reporting](https://github.com/lukaszgard/den/security/advisories/new)
(Security → Advisories → Report a vulnerability). Do not open a public issue for it.

Include what you found, how to reproduce it and which den version it affects
(`den --version`). You will get an answer within a few working days, and a fix is
released as soon as it is ready and credited to you if you wish.

## What counts

den handles IAM credentials, tunnels and secrets, so these matter most:

- a credential, token or secret that leaves den: in `den list --json`, an MCP result, a
  log, the clipboard longer than documented, or a command line other users can read;
- a way for an AI agent to connect a production service, or run a runbook, without the
  confirmation `den.yaml` promises;
- a way to run code through `den.yaml`, a runbook source or a downloaded update without
  the user's approval or the checksum check;
- the privilege helper (`den vpn install-helper`) doing more than starting and stopping
  the configured VPNs.

The [security model](docs/security/README.md) says what den is meant to guarantee.

## Supported versions

Only the latest release gets fixes.
