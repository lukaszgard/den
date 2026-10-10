# Neptune

Gremlin, openCypher and SPARQL, signed for you

## What it is

An Amazon Neptune cluster with IAM database authentication, reached through a tunnel. Every request and WebSocket handshake must be signed for the cluster's own host and port, which a client on `localhost` cannot do. den runs the same signing proxy as for OpenSearch: your clients talk plain HTTP or WebSocket to `127.0.0.1`, and the proxy signs for `neptune-db` as you and verifies the cluster's certificate.

## What you can do

- Query with `curl`: openCypher, Gremlin over HTTP and SPARQL, on `localhost`.
- Point the Gremlin console at the proxy, with no TLS or signing plugin: WebSocket upgrades pass through.
- Open your own shell in a terminal tab inside den, with `DEN_URL` set to the proxy.
- See failed requests in the detail view's log.
- Keys: press `h` in the detail view.

## Before you start

- `curl`, or your Gremlin, openCypher or SPARQL client (`den doctor --for neptune`).
- IAM database authentication turned on for the cluster; without it Neptune ignores the signature.
- IAM: `neptune-db:connect`, or the finer `neptune-db:ReadDataViaQuery` and related actions, on the cluster's resource ID for the `credential_profile` identity.
- The bastion must reach the cluster on its port, 8182 by default.

## Configure

A service of `type: neptune` with a `neptune:` section; the shared keys are in Services.

- `neptune_host` (required): the cluster, reader or instance endpoint, without scheme or port.
- `port` (default `8182`): the cluster's port, if it is not the default.
- `local_port` (default `58182`): where the proxy listens on `127.0.0.1`.

The cluster endpoint:

```yaml
services:
  - name: graph dev
    type: neptune
    env: dev
    neptune:
      neptune_host: graph-dev.cluster-abc123.eu-central-1.neptune.amazonaws.com
      local_port: 58182
```

The reader endpoint of a cluster on a non-default port, reopened after a drop:

```yaml
services:
  - name: graph prod (reader)
    type: neptune
    env: prod
    neptune:
      neptune_host: graph-prod.cluster-ro-def456.eu-central-1.neptune.amazonaws.com
      port: 8183
      local_port: 58183
      reconnect: true
```

A Gremlin console `conf/remote.yaml` for the proxy (not a `den.yaml` snippet):

```
hosts: [localhost]
port: 58182
connectionPool: { enableSsl: false }
serializer: { className: org.apache.tinkerpop.gremlin.util.ser.GraphBinaryMessageSerializerV1 }
```

## Limitations

- Anything that can reach the proxy acts as you: it listens on `127.0.0.1` only.
- A reader endpoint balances by DNS: each connect lands on one instance, and a failover breaks it until you connect again.
- The shell tab gets no credential: only requests through `$DEN_URL` are signed.

## When it goes wrong

- `AccessDeniedException` or `403`: signing worked, but the role lacks the `neptune-db` action on this cluster's resource ID.
- `401 Missing Authentication Token`: the request bypassed the proxy; point the client at `localhost` and the local port.
- Queries hang on a reader endpoint: the instance behind it went away; connect again.

## See also

- OpenSearch
- Services
- Terminal tabs
- `docs/neptune/README.md`
