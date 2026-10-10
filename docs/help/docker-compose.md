# Docker Compose

Start a local Compose stack from den

## What it is

A service of type `docker` is a local Docker Compose file: connecting it runs `docker compose up -d`, for all of its services or the ones you list. Use it for the local databases and queues your work needs next to the cloud ones, so one dashboard starts both.

## What you can do

- Start a Compose stack from the Connect list or the Create Env panel, and see `docker compose` output in its log.
- Start only some of the file's services, and pass an env file.
- Start the same kind of stack from a script with `den create-env -f FILE`.
- Keys: press `h` in the Connect panel.

## Before you start

- Docker with the Compose plugin, and the Docker daemon running (`den doctor --for docker`).
- The compose file and any env file it uses.
- No AWS login: nothing here is tunnelled.

## Configure

A service of `type: docker` with a `docker:` section. It takes none of the shared tunnel keys.

- `compose_file` (required): the Compose file, relative to where den runs or absolute.
- `services`: the Compose services to start; every one when left out.
- `env_file`: passed to `docker compose` with `--env-file`.

The whole stack:

```yaml
services:
  - name: local stack
    type: docker
    docker:
      compose_file: ./docker-compose.yml
```

Two of its services, with local settings:

```yaml
services:
  - name: local db and cache
    type: docker
    docker:
      compose_file: ./docker-compose.yml
      services: [postgres, redis]
      env_file: ./.env.local
```

## Limitations

- Disconnecting only marks the service stopped: the containers keep running. Stop them with `docker compose down`.
- There is no health check: the service counts as connected once `docker compose up -d` returns.
- No terminal tabs, credential or local port of its own: the ports are whatever the Compose file publishes.

## When it goes wrong

- `Cannot connect to the Docker daemon`: start Docker Desktop, Colima or the daemon.
- `no such service`: a name in `services` is not in the Compose file.

## See also

- Services
- Command line
