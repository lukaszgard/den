---
name: add-transport
description: This skill should be used when the user asks to "add a transport", "support another way to reach the VPC", "tunnel through <X> instead of SSM", or mentions a new tunnelling method for den (e.g. "Teleport", "Tailscale", "AWS Client VPN", "gcloud IAP").
---

# Add a transport to den

A transport is how den reaches the network a service lives in (`ssm`, `ssh`, `eice`,
`kubectl`). It only builds the command that exposes one `Forward` on localhost; the
`ssmfwd.Tunnel` handles readiness (port accepting connections), retries, credentials,
monitoring and reconnects for every transport alike.

## Steps

1. **Config** (`internal/config/schema.go`) — a `Transport<Name>` constant, the value
   in the `enum=` list on `TransportSpec.Type`, and any new keys on `TransportSpec`
   with a comment naming the transport they belong to.
2. **Validation** (`validateTransport` in `internal/config/config.go`) — the keys the
   type needs, and rejection of keys that belong to another type. If the transport
   needs no bastion instance, check `ResolveEnv` and the RDS "needs instance" rule
   still hold.
3. **Implementation** (`internal/service/ssmfwd/transport.go`) — a type with `Name()`
   (shown in logs and errors), `Command(ctx, Forward)` and `ReadyTimeout()`, and a case
   in `NewTransport`. Bind the local end to `127.0.0.1`. Fail fast rather than prompt:
   nothing can answer a prompt (see `BatchMode` in `sshTransport`).
4. **Doctor** (`internal/doctor/needs.go`) — the tool the transport runs, and whether
   it needs AWS or a bastion.
5. **Tests**
   - `transport_test.go`: exact `Command` arguments.
   - `lifecycle_test.go`: teach `TestHelperProcess` how the command spells the local
     port, then drive a real `Tunnel` with `fakeBinary(t, "<program>")` as in
     `TestSSHAndKubectlTransportsConnect`.
   - `internal/config/config_test.go` `TestTransportValidation` cases.
6. **Docs** — the table and prerequisites in `docs/transports/README.md`, the keys and
   an example in `docs/help/transports.md` (`helpdoc_test.go` fails without one), a
   commented environment in `den.yaml`, `make schema`.
7. `make check`.
