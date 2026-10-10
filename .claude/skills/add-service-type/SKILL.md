---
name: add-service-type
description: This skill should be used when the user asks to "add a service type", "support <database/cache> in den", "add a new connection type", "add an ssmfwd engine", or wants den to reach a new kind of data store (e.g. "add DynamoDB DAX", "support Aurora DSQL", "implement Kafka/MSK").
---

# Add a service type to den

A service type is a kind of data store den connects (rds, redis, documentdb, …).
Anything reached through a forward is an `ssmfwd.Engine`; the shared `ssmfwd.Tunnel`
already owns the process, readiness, retries, credential refresh, monitor and
reconnect — never copy that lifecycle.

## Steps

1. **Config** (`internal/config/schema.go`)
   - Add `ServiceType<Name>` to the `ServiceType` constants and to the `enum=` list on
     `ServiceSpec.Type`.
   - Add a spec struct embedding `SSMTarget` inline (`yaml:",inline"`) plus the
     type's own keys, each with a trailing comment (it becomes the schema description).
     Mark required keys `jsonschema:"required"` only when they cannot come from an
     environment.
   - Add the field to `ServiceSpec` and a case to `ServiceSpec.Target()`.
2. **Validation** (`internal/config/config.go`) — a case in `Validate`'s type switch
   that rejects a missing section and missing required keys; defaults in
   `applyDefaults` if any.
3. **Engine** (`internal/service/<type>/<type>.go`) — `New(name, spec)` calls
   `spec.ResolveEnv()`, applies a default `LocalPort`, and returns `ssmfwd.New(...)`
   with an engine implementing `Type`, `UserPort`, `Forwards`, `ConnectCommand`, and as
   needed `Credentialer` (mint + side effects such as pgpass), `Preparer` (CA bundle,
   local proxy — see `sigv4proxy`), `SecretCommander` (the `Y` command),
   `TerminalCommander` (the `t` terminal tab: the client as an argument list built
   from the same parts as `ConnectCommand`, the credential in `Env`, never in `Args`;
   `service.ShellTool` when the store has no client to log in). Read
   `internal/service/rds` and `internal/service/redshift` first.
4. **Registry** — add the type constant and label in `internal/service/service.go`
   (`Type*`, `typeLabels`), an icon in `internal/tui/components/statusbadge.go`, and a
   factory in `serviceFactories` (`cmd/den/services.go`).
5. **Tooling around it**
   - `internal/doctor/needs.go` `clientTool`: the client the connect command runs.
   - `internal/prereq`: a `Catalog` entry for that client if it is new (install
     recipes for macOS, Debian, Fedora and Windows — or an `Unsupported` note —
     checked against the vendor's docs, a version pattern, `MinVersion` if den relies
     on a newer flag) and a `Features` entry named after the type.
     `TestEveryServiceTypeIsAFeature` and `TestCatalogIsComplete` fail until both exist.
   - `internal/mcpserver/server.go` `credentialHint`: how a client authenticates.
   - `internal/discover`: discovery and generation, if AWS can list the resource.
6. **Tests** — engine `Forwards`/`ConnectCommand`/credential logic in the package
   (see `internal/service/documentdb/documentdb_test.go`); a `Validate` case in
   `internal/config/config_test.go`.
7. **Docs** — follow the `feature-docs` skill: `docs/<type>/README.md` + `.tape`, a
   commented example in `den.yaml`, the README "Supported Services" table and service
   fields table, and a Help topic `docs/help/<type>.md` listed under Data stores in
   `docs/help/README.md` (`helpdoc_test.go` fails until the new type has a snippet;
   `topics_test.go` holds it to the template).
8. `make generate && make check` (regenerates the schema, `docs/config`, `docs/cli` and `docs/doctor/install.md`).

## Pitfalls

- IAM tokens sign the real host and port, not `localhost` and the local port.
- A store behind several nodes needs several `Forward`s; clients that follow
  advertised addresses (Kafka, Redis cluster mode) need more than a forward — say so
  in the docs rather than shipping something that half works.
- Keep credentials out of `ConnectCommand()`: `y` copies it verbatim.
