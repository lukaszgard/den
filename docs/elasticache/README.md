# ElastiCache / Valkey

Opens an SSM port-forward to an **Amazon ElastiCache** cache (Valkey or Redis OSS),
provisioned or Serverless, through the bastion host. With an IAM-enabled cache user
it authenticates with a short-lived **IAM auth token** instead of a password; without
one it opens the tunnel and nothing more.

ElastiCache is a `redis` service. den mints the token with a presigned URL for the
`elasticache` service and re-mints it every 13 minutes while the tunnel is up (tokens
live 15). [MemoryDB](../memorydb/README.md) is the same service type with
`memorydb: true`: the token is signed for `memorydb` instead.

## Configuration

```yaml
services:
  # A provisioned cache with IAM auth
  - name: "cache dev"
    type: redis
    env: dev
    redis:
      redis_host: master.cache-dev.abc123.euc1.cache.amazonaws.com
      cache_name: cache-dev        # the replication group ID
      user_id: app-iam-user        # the cache user, authentication mode "iam"
      local_port: 16379

  # A Serverless cache, reopened after a drop
  - name: "rate limits dev"
    type: redis
    env: dev
    redis:
      redis_host: rate-limits-dev-abc123.serverless.euc1.cache.amazonaws.com
      cache_name: rate-limits-dev  # the Serverless cache name
      user_id: app-iam-user
      serverless: true
      local_port: 16381
      reconnect: true

  # A cache without IAM auth: den opens the tunnel and you log in as you always do
  - name: "legacy cache dev"
    type: redis
    env: dev
    redis:
      redis_host: master.legacy-dev.abc123.euc1.cache.amazonaws.com
      local_port: 16382
```

| Field        | Required | Description                                                                                  |
|--------------|----------|----------------------------------------------------------------------------------------------|
| `redis_host` | yes      | Endpoint to forward to: a provisioned cache's primary endpoint, or a Serverless cache's endpoint |
| `cache_name` | for IAM  | The replication group ID, or the Serverless cache name. It is the host the token is signed for |
| `user_id`    | for IAM  | The cache user to log in as (authentication mode `iam`)                                       |
| `serverless` | no       | `true` for ElastiCache Serverless. Cannot be combined with `memorydb`                         |
| `local_port` | no       | Local end of the tunnel (default `16379`)                                                     |
| `reconnect`  | no       | Auto-reopen the SSM port-forward when it drops (e.g. SSM idle timeout). The token is renewed while connected either way |

Without both `cache_name` and `user_id` den mints no token. The `env:` reference
supplies the bastion, `aws_profile` (opens the SSM session), `credential_profile` (**the
identity the token is signed as**) and `aws_region_code`; every key of the entry is in
the [configuration reference](../config/README.md#servicesredis).

## Prerequisites

- **AWS CLI v2** and the **Session Manager plugin**; a valid SSO session.
- **`redis-cli` 6 or newer, built with TLS** (`den doctor --for redis`). The connect
  command always uses `--tls`, and IAM auth needs `--user`, added in redis-cli 6.
- **For IAM auth:** Valkey, or Redis OSS 7.0 or newer, and a cache user with
  authentication mode `iam` in a user group attached to the cache.
- **IAM:** `elasticache:Connect` on **both** the cache (replication group or Serverless
  cache) and the user ARN, for the `credential_profile` identity.
- **In-transit encryption** on the cache.

## Usage

1. Launch `den`, open **Connect**, select the cache and press `c`. den opens the
   tunnel, checks the local port accepts connections, then mints the token.
2. Press `y` to copy the connect command. The token is not in it:
   ```bash
   redis-cli -h localhost -p 16379 --tls --user app-iam-user
   ```
   `Y` copies the same command with the token (`-a "<token>"`), for use on another
   machine, and `p` copies only the token. `r` re-mints it now.
3. Or stay in den: in the detail view, `t` runs `redis-cli` in a
   [terminal tab](../terminal/README.md) next to the logs, already logged in. The
   token reaches it through `REDISCLI_AUTH`, not the command line.
4. `d` disconnects. It also stops the clients in the service's terminal tabs.

## Limitations

- **One tunnel reaches one node.** A cluster-mode cache with several shards answers
  keys on other shards with `MOVED` to addresses only the VPC can reach, and
  `redis-cli -c` cannot follow them through the tunnel.
- **The client command always uses `--tls`.** A cache without in-transit encryption
  needs it removed from the copied command.
- **A token is checked when a connection opens.** A client that reconnects by itself
  later reuses an expired token; start it again.
- **AI agents through [`den mcp`](../mcp/README.md) cannot use a cache with IAM auth:**
  den never hands them the token.

## Troubleshooting

- **`WRONGPASS invalid username-password pair`:** the token reached the cache, which
  refused it. Check the user's authentication mode, that its user group is attached to
  the cache, and `elasticache:Connect` on both ARNs. For MemoryDB, check
  `memorydb: true` is set: a token signed for the wrong service is rejected.
- **`MOVED … 10.x.x.x:6379`:** the key lives on another shard; see Limitations.
- **The tunnel is up but `redis-cli` cannot connect:** the cache has in-transit
  encryption off, or `redis-cli` was built without TLS. `redis-cli --version` and
  `den doctor --for redis` say which.
