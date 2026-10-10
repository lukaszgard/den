# MemoryDB

Opens an SSM port-forward to an **Amazon MemoryDB** (Valkey / Redis OSS) cluster
through the bastion host and authenticates with a short-lived **IAM auth token**
instead of a MemoryDB user password.

MemoryDB is a `redis` service with `memorydb: true`. The token uses the same
presigned-URL scheme as [ElastiCache](../elasticache/README.md) IAM auth. The only difference is that it is
signed for the `memorydb` service rather than `elasticache`, and a token signed
for the wrong service is simply rejected. den re-mints it every 13 minutes (tokens
live 15).

## Configuration

```yaml
services:
  - name: "EU DEV MemoryDB"   # type stays redis — naming it keeps MemoryDB findable with /
    type: redis
    env: eu-dev
    redis:
      memorydb: true
      local_port: 50181
      reconnect: true
      redis_host: orders-dev-0001-001.orders-dev.abc123.memorydb.eu-central-1.amazonaws.com
      cache_name: orders-dev      # the MemoryDB cluster name
      user_id: iam-user-01      # MemoryDB user with authentication mode "iam"
```

| Field        | Required | Description                                                                                  |
|--------------|----------|----------------------------------------------------------------------------------------------|
| `memorydb`   | yes      | `true` selects MemoryDB token signing. Cannot be combined with `serverless`                  |
| `redis_host` | yes      | Endpoint to forward to. Use the shard's **primary node** endpoint, see [Limitations](#limitations) |
| `cache_name` | yes      | Cluster name. It is the host the token is signed for                                         |
| `user_id`    | yes      | MemoryDB user name (IAM authentication mode)                                                 |
| `local_port` | no       | Local end of the tunnel (default `16379`)                                                    |
| `reconnect`  | no       | Auto-reopen the SSM port-forward when it drops (e.g. SSM idle timeout). The token is renewed while connected either way |

Without `cache_name` and `user_id` den still opens the tunnel but mints no token.
The `env:` reference supplies the bastion, `aws_profile` (opens the SSM session),
`credential_profile` (**the identity the token is signed as**) and `aws_region_code`.

## Prerequisites

- **AWS CLI v2** and the **Session Manager plugin**; a valid SSO session.
- **Valkey or Redis OSS 7.0+** on the cluster. IAM auth does not exist below that.
- **A MemoryDB user in IAM mode, attached to the cluster's ACL:**
  ```bash
  aws memorydb create-user --user-name iam-user-01 \
    --authentication-mode Type=iam --access-string "on ~* +@all"
  aws memorydb update-acl --acl-name <acl> --user-names-to-add iam-user-01
  ```
- **IAM:** `memorydb:Connect` on **both** the cluster and the user ARN
  (`arn:aws:memorydb:<region>:<account>:cluster/<name>` and `…:user/iam-user-01`).
- **`redis-cli`** with TLS support. MemoryDB always requires TLS.

## Usage

1. Launch `den`, open **Connect**, select the MemoryDB service and press `c`.
2. Press `y` to copy the command. It includes the current token:
   ```bash
   redis-cli -h localhost -p 50181 --tls --user iam-user-01 -a "<token>"
   ```
3. `p` copies only the token, and `r` re-mints it now.
4. Or skip the copying: in the detail view, `t` runs `redis-cli` in a
   [terminal tab](../terminal/README.md) next to the logs, already logged in. The
   token reaches it through `REDISCLI_AUTH`, not the command line.
5. `d` disconnects. It also stops the clients in the service's terminal tabs.

## Limitations

- **Single-shard clusters only.** MemoryDB always runs in cluster mode. With
  several shards, keys on other shards answer with `MOVED <private-ip>:6379`, an
  address that only exists inside the VPC. Don't use `redis-cli -c`; its
  redirects cannot follow through the tunnel.
- **Point `redis_host` at the primary node.** The cluster endpoint
  (`clustercfg.…`) can resolve to a replica, which answers writes with `MOVED`.
  Find the primary with
  `aws memorydb describe-clusters --cluster-name <name> --show-shard-details`.
  After a failover the primary moves and `redis_host` needs updating.
- **Connections last at most 12 hours.** MemoryDB disconnects IAM-authenticated
  connections after 12h. Reconnect with a fresh token (`y` again).
