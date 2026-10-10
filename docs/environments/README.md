# Environments

## What it is

An environment says how den gets into one network: the bastion to tunnel through, the
AWS profile that opens the tunnel, the profile the database credential is minted as,
and the region. Declare each one once under `environments:`, then point services (and
runbooks) at it with `env:` instead of repeating those keys, so moving to a new bastion
is one edit.

Anything a service sets itself overrides the environment's value. A service that names
no `env:` can set the same keys inline. den has no built-in knowledge of any account:
everything it connects to is written in `den.yaml`.

`credential_profile` is optional. When it is left out, the credential is minted as
`aws_profile`, so an account with one role for both needs a single key.

## Configuration

A single account with one role, the shortest form:

```yaml
environments:
  dev:
    ec2_instance_id: "i-0123456789abcdef0"
    aws_profile: "acme-dev"                # opens the tunnel and mints the credential
    aws_region_code: "eu-central-1"

services:
  - name: "Dev Orders DB"
    type: rds
    env: dev
    rds:
      rds_host: "db1.abcdefghijkl.eu-central-1.rds.amazonaws.com"
      db_user: "app"
```

Split roles, where the role that may open a tunnel is not the identity the data store knows:

```yaml
aws:
  config_path: "~/.aws/config"
  credentials_path: "~/.aws/credentials"

environments:
  eu-dev:
    region: eu                              # labels you choose; den shows them
    environment: dev                        # and hands them to runbooks
    ec2_instance_id: "i-0123456789abcdef0"
    aws_profile: "acme-dev"                 # opens the SSM tunnel
    credential_profile: "acme-data-dev"     # mints the credential
    aws_region_code: "eu-central-1"

  eu-prod:
    region: eu
    environment: prod                       # prod and production mean production
    ec2_instance_id: "i-0123456789abcdef2"
    aws_profile: "acme-prod"
    credential_profile: "acme-data-prod"
    aws_region_code: "eu-central-1"

services:
  - name: "EU DEV Orders DB"
    type: rds
    env: eu-dev
    rds:
      rds_host: "db1.abcdefghijkl.eu-central-1.rds.amazonaws.com"
      db_user: "app"
      local_port: 50432
      update_pgpass: true
      reconnect: true

  # Any key of the environment can be replaced on one service
  - name: "EU DEV Payments DB"
    type: rds
    env: eu-dev
    rds:
      credential_profile: "acme-payments-dev"
      rds_host: payments.cluster-abcdefghijkl.eu-central-1.rds.amazonaws.com
      local_port: 50436
      db_user: payments_iam

  - name: "Local Stack"
    type: docker
    docker:
      compose_file: ./docker-compose.yml
```

| Key               | Description                                                              |
|-------------------|--------------------------------------------------------------------------|
| `region`          | A label you choose, such as `eu` or `us`. Shown in den and given to runbooks as `DEN_REGION`; den does not interpret it |
| `environment`     | A label you choose, such as `dev` or `staging`. `prod` and `production` (any case) mark the environment as production |
| `production`      | `true` marks the environment as production whatever it is called: AI agents can then connect its services only if you allow it and confirm each connect |
| `ec2_instance_id` | The SSM-managed bastion. Only the `ssm` [transport](../transports/README.md) needs it |
| `aws_profile`     | The profile that opens the tunnel                                        |
| `credential_profile` | The profile the credential is minted as, and the identity the data store sees. `aws_profile` when left out |
| `aws_region_code` | The AWS region, such as `eu-central-1`                                   |
| `transport`       | How den reaches the network; SSM when left out. See [Transports](../transports/README.md) |

Every service shares these keys, plus `local_port` (where the tunnel lands on your
machine) and `reconnect` (reopen the forward after a drop, e.g. the SSM idle timeout;
the credential is renewed while connected either way). Each type adds its own, on its
page: [RDS](../rds/README.md), [ElastiCache](../elasticache/README.md),
[MemoryDB](../memorydb/README.md), [DocumentDB](../documentdb/README.md),
[Redshift](../redshift/README.md), [OpenSearch](../opensearch/README.md),
[Neptune](../neptune/README.md); `docker` services have no tunnel and so none of these.
All keys are in the [configuration reference](../config/README.md#environmentsname).

The committed [`den.yaml`](../../den.yaml) is a fuller working example, including
commented entries for MySQL, MemoryDB, Kafka and Docker.

## Prerequisites

- The AWS profiles in your AWS config (`~/.aws/config`) and a login for them;
  `den doctor` checks both.
- For the default SSM transport: an EC2 instance in the network with the SSM agent
  online, and a profile allowed to start a port-forwarding session on it.

## Limitations

- An environment holds connection settings only: ports, hosts and users stay on each
  service.
- A service that sets its own `transport:` replaces the environment's whole block; the
  keys are not merged.
- Production is recognised by `production: true` or an `environment` of `prod` or
  `production`; a name like `live` needs the `production: true` key.
- An environment cannot inherit from another one.

## Troubleshooting

- **`unknown env "x"`:** the service's `env:` names an environment that is not in
  `environments:`. Names are case-sensitive.
- **`TargetNotConnected` from SSM:** the bastion's SSM agent is offline, or
  `ec2_instance_id` is in another account or region. `den doctor --deep` asks SSM about
  every bastion.
- **The credential is minted but the database rejects it:** the database checks
  `credential_profile` (or `aws_profile` when that is left out), not the profile that
  opened the tunnel. Grant that identity.
- **`ec2_instance_id is required for the ssm transport`:** the SSM transport needs a
  bastion instance. The other transports do not.
