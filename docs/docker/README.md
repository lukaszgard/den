# Docker Compose

Starts a local **Docker Compose** stack from den, next to the cloud services. A service
of `type: docker` is a Compose file: connecting it runs `docker compose up -d` for all
of its services, or the ones you list. Use it for the local databases and queues your
work needs, so one dashboard starts both.

Nothing here is tunnelled: there is no AWS login, no credential and no local port of
den's own. The ports are whatever the Compose file publishes.

## Configuration

```yaml
services:
  # The whole stack
  - name: "local stack"
    type: docker
    docker:
      compose_file: ./docker-compose.yml

  # Two of its services, with local settings
  - name: "local db and cache"
    type: docker
    docker:
      compose_file: ./docker-compose.yml
      services: [postgres, redis]
      env_file: ./.env.local
```

| Field          | Required | Description                                                                 |
|----------------|----------|-----------------------------------------------------------------------------|
| `compose_file` | yes      | The Compose file, relative to where den runs or absolute                    |
| `services`     | no       | The Compose services to start; every one in the file when left out          |
| `env_file`     | no       | Passed to `docker compose` with `--env-file`                                |

A `docker` entry takes none of the shared tunnel keys (`env`, `local_port`,
`transport`, …); the [configuration reference](../config/README.md#servicesdocker) lists
the keys.

## Prerequisites

- **Docker with the Compose plugin**, and the Docker daemon running
  (`den doctor --for docker`).
- The Compose file and any env file it uses.

## Usage

1. Launch `den`, open **Connect**, select the stack and press `c`. den runs
   `docker compose -f FILE up -d` and shows its output in the service's log.
2. The service is connected once `docker compose up -d` returns. `y` copies
   `docker compose ps`, to see what runs.
3. The **Create Env** panel lists only the `docker` services of den.yaml and brings
   them up the same way.
4. Without a den.yaml entry: `den create-env -f FILE` starts a Compose file and opens
   the dashboard on it. The file defaults to `docker-compose.yml`;
   [compose.yml](compose.yml) next to this page is a one-service example.

## Limitations

- **Disconnecting only marks the service stopped:** the containers keep running. Stop
  them with `docker compose down`.
- **There is no health check:** the service counts as connected as soon as
  `docker compose up -d` returns, whether or not the containers stay up.
- No terminal tabs and no credential: the stack is yours to connect to.

## Troubleshooting

- **`Cannot connect to the Docker daemon`:** start Docker Desktop, Colima or the daemon.
- **`no such service`:** a name in `services` is not in the Compose file.
- **`docker: 'compose' is not a docker command`:** Docker is too old or lacks the
  Compose plugin. `den doctor --for docker` checks Docker and prints how to install it;
  on Linux, Docker's own install script includes the plugin.
