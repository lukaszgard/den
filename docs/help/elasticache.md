# ElastiCache and Valkey

Redis-compatible caches, with IAM tokens or none

## What it is

An ElastiCache cache (Valkey or Redis OSS), provisioned or Serverless, reached through a tunnel. With an IAM-enabled cache user, den mints the short-lived token the cache accepts instead of a password, and mints a new one before it runs out; without one, den opens the tunnel and nothing more.

## What you can do

- Connect a cache from the Connect panel and use it with `redis-cli` on `localhost`.
- Log in with IAM: den mints a token for your cache user and renews it every 13 minutes while connected (a token lives 15).
- Copy the `redis-cli` command, the same command with the token, or the token alone, from the detail view.
- Open `redis-cli` in a terminal tab inside den, already logged in; the token reaches it through `REDISCLI_AUTH`, not the command line.
- Reach a cache without IAM auth too: den then opens only the tunnel.
- Keys: press `h` in the detail view.

## Before you start

- `redis-cli` 6 or newer, built with TLS (`den doctor --for redis`).
- For IAM auth: Valkey, or Redis OSS 7.0 or newer; a cache user with authentication mode `iam`, in a user group attached to the cache.
- IAM: `elasticache:Connect` on both the cache and the user, for the `credential_profile` identity.
- In-transit encryption on the cache: den's client command always uses TLS.

## Configure

A service of `type: redis` with a `redis:` section; the shared keys are in Services.

- `redis_host` (required): the endpoint to forward to: a provisioned cache's primary endpoint, or a Serverless cache's endpoint.
- `cache_name`: the replication group ID or the Serverless cache name; the host the token is signed for.
- `user_id`: the cache user to log in as. With `cache_name`, den mints IAM tokens; without both, it opens only the tunnel.
- `serverless` (default `false`): `true` for ElastiCache Serverless.
- `local_port` (default `16379`): where the cache appears on `127.0.0.1`.

A provisioned cache with IAM auth:

```yaml
services:
  - name: cache dev
    type: redis
    env: dev
    redis:
      redis_host: master.cache-dev.abc123.euc1.cache.amazonaws.com
      cache_name: cache-dev
      user_id: app-iam-user
      local_port: 16379
```

A Serverless cache, reopened after a drop:

```yaml
services:
  - name: rate limits dev
    type: redis
    env: dev
    redis:
      redis_host: rate-limits-dev-abc123.serverless.euc1.cache.amazonaws.com
      cache_name: rate-limits-dev
      user_id: app-iam-user
      serverless: true
      local_port: 16381
      reconnect: true
```

A cache with no IAM user: den opens the tunnel and you log in as you always do.

```yaml
services:
  - name: legacy cache dev
    type: redis
    env: dev
    redis:
      redis_host: master.legacy-dev.abc123.euc1.cache.amazonaws.com
      local_port: 16382
```

## Limitations

- One tunnel reaches one node: a cluster-mode cache with several shards answers keys on other shards with `MOVED` to addresses only the VPC can reach, and `redis-cli -c` cannot follow them.
- The client command always uses `--tls`; a cache without in-transit encryption needs it removed from the copied command.
- A token is checked when a connection opens: a client that reconnects by itself later reuses an expired token. Start it again.
- AI agents through `den mcp` cannot use a cache with IAM auth: den never hands them the token.

## When it goes wrong

- `WRONGPASS invalid username-password pair`: the token reached the cache, which refused it; check the user's authentication mode, its user group, and `elasticache:Connect` on both ARNs.
- `MOVED … 10.x.x.x:6379`: the key lives on another shard; see Limitations.

## See also

- MemoryDB
- Services
- Terminal tabs
