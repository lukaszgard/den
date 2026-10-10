# MemoryDB

MemoryDB clusters with IAM tokens

## What it is

An Amazon MemoryDB cluster (Valkey or Redis OSS) reached through a tunnel, logged in to with a short-lived IAM token instead of a user password. It is a `redis` service with `memorydb: true`: the token works like ElastiCache's but is signed for MemoryDB, which rejects one signed for the other.

## What you can do

- Connect a cluster from the Connect panel and use it with `redis-cli` on `localhost`.
- Log in with IAM: den mints the token and renews it every 13 minutes while connected (a token lives 15).
- Copy the `redis-cli` command, with the current token, or the token alone, from the detail view.
- Open `redis-cli` in a terminal tab inside den, already logged in.
- Keys: press `h` in the detail view.

## Before you start

- `redis-cli` 6 or newer, built with TLS (`den doctor --for redis`); MemoryDB always requires TLS.
- Valkey, or Redis OSS 7.0 or newer, on the cluster: IAM auth does not exist below that.
- A MemoryDB user with authentication mode `iam`, in the access control list of the cluster.
- IAM: `memorydb:Connect` on both the cluster and the user, for the `credential_profile` identity.

## Configure

A service of `type: redis` with `memorydb: true` in its `redis:` section; the shared keys are in Services.

- `memorydb` (required): `true`; it cannot be combined with `serverless`.
- `redis_host` (required): the primary node of the shard, not the cluster endpoint.
- `cache_name` (required): the cluster name; the host the token is signed for.
- `user_id` (required): the MemoryDB user.
- `local_port` (default `16379`): where the cluster appears on `127.0.0.1`.

A single-shard cluster:

```yaml
services:
  - name: leaderboard dev (MemoryDB)
    type: redis
    env: dev
    redis:
      memorydb: true
      redis_host: leaderboard-0001-001.leaderboard.abc123.memorydb.eu-central-1.amazonaws.com
      cache_name: leaderboard
      user_id: iam-user-01
      local_port: 16383
```

The production cluster next to it, reopened after a drop:

```yaml
services:
  - name: leaderboard prod (MemoryDB)
    type: redis
    env: prod
    redis:
      memorydb: true
      redis_host: leaderboard-0001-001.leaderboard.def456.memorydb.eu-central-1.amazonaws.com
      cache_name: leaderboard
      user_id: iam-readonly
      local_port: 16384
      reconnect: true
```

## Limitations

- Single-shard clusters only: MemoryDB always runs in cluster mode, and keys on other shards answer with `MOVED` to addresses only the VPC can reach.
- `redis_host` must be the shard's primary node: the cluster endpoint can resolve to a replica, which answers writes with `MOVED`. After a failover the primary moves, and `redis_host` must follow it.
- MemoryDB closes IAM connections after 12 hours: connect the client again with a fresh token.
- AI agents through `den mcp` cannot use it: den never hands them the token.

## When it goes wrong

- `WRONGPASS`: the token was refused. Check the user's authentication mode, that it is in the cluster's access control list, and `memorydb:Connect` on both ARNs.
- `MOVED` on a write: `redis_host` points at a replica. Find the primary with `aws memorydb describe-clusters --cluster-name NAME --show-shard-details`.

## See also

- ElastiCache and Valkey
- Services
- Terminal tabs
- `docs/memorydb/README.md`
