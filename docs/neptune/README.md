# Neptune

Reaches an **Amazon Neptune** cluster with IAM database authentication through the bastion
host, so `curl`, the Gremlin console, openCypher and SPARQL clients can query it without
knowing anything about SigV4.

Neptune is VPC-only, and with IAM auth enabled every HTTP request and every WebSocket
handshake must be SigV4-signed for the cluster's `host:port`. Through a tunnel the
client talks to `localhost`, so the signature and TLS verification no longer line up.
den runs a **local signing proxy** in front of the tunnel, the same one the
[OpenSearch](../opensearch/README.md) service uses:

```
client ──http/ws──▶ den proxy :50171 ──https/wss (SigV4)──▶ SSM tunnel ──▶ cluster :8182
                    (127.0.0.1 only)
```

The proxy sets `Host` to `<cluster>:8182`, signs for `neptune-db` as the
`credential_profile` identity, and dials the tunnel with TLS `ServerName` set to the
cluster endpoint, so the certificate is **verified**. WebSocket upgrades pass through,
which is what the Gremlin console uses.

The proxy listens on `127.0.0.1` only. Anything that can reach it acts with your IAM
identity.

## Configuration

```yaml
services:
  - name: "EU DEV"
    type: neptune
    env: eu-dev
    neptune:
      local_port: 50171
      reconnect: true
      neptune_host: graph-int.cluster-abc123xyz.eu-central-1.neptune.amazonaws.com
      # port: 8182            # only if the cluster uses a non-default port
```

| Field          | Required | Description                                                       |
|----------------|----------|-------------------------------------------------------------------|
| `neptune_host` | yes      | Cluster, reader or instance endpoint, without scheme or port      |
| `port`         | no       | Cluster port (default `8182`)                                     |
| `local_port`   | no       | Where the proxy listens (default `58182`)                         |
| `reconnect`    | no       | Auto-reopen the SSM port-forward when it drops (e.g. SSM idle timeout) |

The `env:` reference supplies the bastion, `aws_profile` (opens the SSM session),
`credential_profile` (**the identity requests are signed as**) and `aws_region_code`.

## Prerequisites

- **AWS CLI v2** and the **Session Manager plugin**; a valid SSO session for both profiles.
- **IAM database authentication enabled** on the cluster. Without it Neptune ignores
  the signature, and the proxy only adds TLS.
- **IAM:** `neptune-db:connect`, or the finer-grained `neptune-db:ReadDataViaQuery` /
  `WriteDataViaQuery` / … actions, on
  `arn:aws:neptune-db:<region>:<account>:<cluster-resource-id>/*` for the signing role.
- **Network:** the bastion must reach the cluster on 8182.

## Usage

1. Launch `den`, open **Connect**, select the Neptune service and press `c`.
2. Press `y` to copy the health check:
   ```bash
   curl -s http://localhost:50171/status
   ```
3. Query the graph. The proxy speaks plain HTTP/WS locally:
   ```bash
   # openCypher
   curl -s http://localhost:50171/openCypher -d 'query=MATCH (n) RETURN n LIMIT 5'
   # Gremlin over HTTP
   curl -s http://localhost:50171/gremlin -d '{"gremlin":"g.V().limit(5)"}'
   # SPARQL
   curl -s http://localhost:50171/sparql --data-urlencode 'query=SELECT * WHERE { ?s ?p ?o } LIMIT 5'
   ```
   **Gremlin console**: point `conf/remote.yaml` at the proxy without TLS or a SigV4
   plugin, because the proxy handles both:
   ```yaml
   hosts: [localhost]
   port: 50171
   connectionPool: { enableSsl: false }
   serializer: { className: org.apache.tinkerpop.gremlin.util.ser.GraphBinaryMessageSerializerV1 }
   ```
   then `:remote connect tinkerpop.server conf/remote.yaml` and `:remote console`.
4. **A shell inside den:** in the detail view, `t` opens your shell in a
   [terminal tab](../terminal/README.md#shell-tabs) next to the logs, with `DEN_URL`
   set to the proxy: `curl -s "$DEN_URL/status"`.
5. `d` disconnects and stops the proxy, and the shells in the service's terminal tabs.

## Troubleshooting

- **`AccessDeniedException` / `403`:** signing worked but the role lacks the `neptune-db:*`
  action for this cluster's resource ID. Failed requests are logged in the detail view.
- **`401` with `Missing Authentication Token`:** the request bypassed the proxy. Check the
  client points at `localhost:<local_port>`, not the cluster endpoint.
- **Queries hang on a reader endpoint:** the reader endpoint load-balances across
  instances by DNS. Each connect picks one, and a failover invalidates it. Reconnect with `c`.
