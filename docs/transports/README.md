# Transports

## What it is

A transport is how den reaches the private network a service lives in. Until
now that was always an SSM port forward through a bastion instance; a
`transport:` block picks another way. Everything else stays the same whichever
transport carries the traffic: den waits for the local port, mints and refreshes
the IAM credential, monitors the forward and reconnects it.

| Type | Runs | Use it when |
|---|---|---|
| `ssm` (default) | `aws ssm start-session` with `AWS-StartPortForwardingSessionToRemoteHost` through `ec2_instance_id` | you have an SSM-managed instance in the VPC |
| `ssh` | `ssh -N -L 127.0.0.1:LOCAL:host:port <host>` | you have an SSH bastion |
| `eice` | `aws ec2-instance-connect open-tunnel` through an EC2 Instance Connect Endpoint | you have an endpoint and no bastion to keep patched |
| `kubectl` | `kubectl port-forward <resource> LOCAL:PORT` | the database is reachable only from inside a Kubernetes cluster, through a proxy there |

## Configuration

Set `transport:` on an environment (every service using it inherits it) or on a
single service's type section:

```yaml
environments:
  lab:
    region: lab
    environment: lab
    credential_profile: "lab"          # still mints the IAM credential
    aws_region_code: "eu-west-1"
    transport:
      type: ssh
      host: ops@bastion.lab.example.com
      args: [-i, ~/.ssh/lab_ed25519, -J, jump.example.com]

services:
  - name: orders
    type: rds
    env: lab
    rds:
      rds_host: orders.cluster-abc.eu-west-1.rds.amazonaws.com
      local_port: 50200
```

| Key | Transport | Meaning |
|---|---|---|
| `type` | all | `ssm`, `ssh`, `eice` or `kubectl` |
| `host` | ssh | destination, `user@host` or an alias from `~/.ssh/config` |
| `args` | ssh | extra ssh arguments (identity file, jump host, port) |
| `endpoint_id` | eice | the EC2 Instance Connect Endpoint ID; the tunnel opens as `aws_profile` in `aws_region_code` |
| `resource` | kubectl | `svc/…`, `deploy/…` or `pod/…` that serves the service's port |
| `namespace`, `context` | kubectl | optional; kubectl's current ones otherwise |

A key that belongs to another type is an error, as is a missing required one:
`den` refuses to start rather than ignore it. Only `ssm` needs `ec2_instance_id`.

## Prerequisites

- **ssh** — key or agent authentication. den runs ssh with `BatchMode=yes`, so a
  password or passphrase prompt fails the connection instead of hanging it.
  `ExitOnForwardFailure=yes` makes a taken port an error rather than a silent
  no-op.
- **eice** — AWS CLI v2 and an endpoint whose security group may reach the
  database port. The endpoint tunnels to IP addresses only, so den resolves the
  service host first; RDS and ElastiCache endpoints resolve to their private
  addresses from anywhere. EC2 Instance Connect tunnels are time-limited, so
  set `reconnect: true` on long-lived services.
- **kubectl** — a working context, and a resource that serves the service's
  port itself, typically a TCP proxy (socat, HAProxy, pgbouncer) in front of the
  database: `kubectl port-forward` cannot reach arbitrary hosts. The IAM token
  is still signed for `rds_host`/`redis_host`, which is what the database checks.

`den doctor` checks that `ssh` or `kubectl` is installed when a transport needs it.

## Usage

Nothing changes: `c` connects, the detail view shows the transport's own output
(`ssh`, `EC2 Instance Connect tunnel`, `kubectl port-forward`), and a drop names
it — e.g. `ssh exited: exit status 255 — Permission denied (publickey)`.
