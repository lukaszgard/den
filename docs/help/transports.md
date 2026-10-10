# Transports

SSM, SSH, EC2 Instance Connect or kubectl

## What it is

A transport is how den gets traffic into the private network a store lives in. The default is an SSM port-forward through a bastion instance; `transport:` picks an SSH jump host, an EC2 Instance Connect Endpoint or a `kubectl port-forward` instead. Whichever carries the traffic, den does the rest the same way: it waits for the local port, mints and renews the credential, watches the forward and reconnects it.

## What you can do

- Tunnel through an SSM-managed instance (`ssm`), with no inbound port and no SSH key.
- Go through an SSH bastion or jump host you already use (`ssh`), with your keys and `~/.ssh/config`.
- Use an EC2 Instance Connect Endpoint (`eice`), with no bastion instance to keep patched.
- Reach a store only a Kubernetes cluster can see (`kubectl`), through a proxy running in the cluster.
- Set a transport for a whole environment, or for one service.
- Read the transport's own output in the service's detail view, where a drop names it, for example `ssh exited: … Permission denied (publickey)`.

## Before you start

- `ssm`: the AWS CLI v2 and the Session Manager plugin; `den doctor --for ssm`.
- `ssh`: `ssh`, and key or agent authentication: den cannot answer a password or passphrase prompt. `den doctor --for ssh`.
- `eice`: the AWS CLI v2, an endpoint whose security group may reach the store's port, and `ec2-instance-connect:OpenTunnel` for the tunnel profile. `den doctor --for eice`.
- `kubectl`: a working kube context, and a resource that serves the store's port itself, typically a TCP proxy such as socat, HAProxy or pgbouncer. `den doctor --for kubectl`.

## Configure

A `transport:` block on an environment, or under a service's type section.

- `type` (required): `ssm`, `ssh`, `eice` or `kubectl`.
- `host` (`ssh`, required): the destination, `user@host` or an alias from `~/.ssh/config`.
- `args` (`ssh`): extra ssh arguments, such as an identity file or a jump host.
- `endpoint_id` (`eice`, required): the EC2 Instance Connect Endpoint ID; the tunnel opens as `aws_profile` in `aws_region_code`.
- `resource` (`kubectl`, required): the `svc/…`, `deploy/…` or `pod/…` that serves the store's port.
- `namespace`, `context` (`kubectl`): kubectl's current ones when left out.

The default, written out: an SSM port-forward through the bastion instance.

```yaml
environments:
  stage:
    region: eu
    environment: stage
    ec2_instance_id: i-0abcdef1234567890
    aws_profile: stage-bastion
    credential_profile: stage-data
    aws_region_code: eu-central-1
    transport:
      type: ssm
```

Through an SSH bastion, with a key and a jump host:

```yaml
environments:
  lab-ssh:
    region: eu
    environment: lab
    credential_profile: lab-data       # still mints the database credential
    aws_region_code: eu-west-1
    transport:
      type: ssh
      host: ops@bastion.lab.example.com
      args: [-i, ~/.ssh/lab_ed25519, -J, jump.example.com]
```

Through an EC2 Instance Connect Endpoint:

```yaml
environments:
  lab-eice:
    region: eu
    environment: lab
    aws_profile: lab-tunnel         # opens the endpoint tunnel
    credential_profile: lab-data
    aws_region_code: eu-west-1
    transport:
      type: eice
      endpoint_id: eice-0123456789abcdef0
```

Through a proxy in a Kubernetes cluster:

```yaml
environments:
  lab-k8s:
    region: eu
    environment: lab
    credential_profile: lab-data
    aws_region_code: eu-west-1
    transport:
      type: kubectl
      resource: svc/db-proxy        # must serve the port itself
      namespace: data
      context: lab-cluster
```

One service through SSH while the rest of its environment uses SSM:

```yaml
services:
  - name: orders dev (via ssh)
    type: rds
    env: dev
    rds:
      rds_host: orders.cluster-abc123.eu-central-1.rds.amazonaws.com
      local_port: 50437
      db_name: orders
      db_user: app_iam
      transport:
        type: ssh
        host: dev-jump               # an alias from ~/.ssh/config
```

## Limitations

- A key of another transport type is an error, as is a missing required one: den refuses to start rather than ignore it.
- `ssh` cannot prompt: password logins and passphrase-protected keys without an agent fail.
- `eice` tunnels to IP addresses only, so den resolves the store's host first, and its tunnels are time-limited: set `reconnect: true` on long-lived services.
- `kubectl port-forward` reaches pods and services, not arbitrary hosts: the store's port must be served inside the cluster.
- User-defined SSH tunnels and the VPN do not use transports; they run their own commands.

## When it goes wrong

- `Permission denied (publickey)`: ssh found no usable key; load it into your agent or name it in `args`.
- With `kubectl` the forward is up but the client gets no answer: the resource does not serve the store's port itself; put a TCP proxy in front of the store inside the cluster.
- An `eice` forward that drops after a while: EC2 Instance Connect tunnels are time-limited; set `reconnect: true`.

## See also

- Environments
- Services
- Tunnels
- `docs/transports/README.md`
