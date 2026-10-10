# Environments

Where services are reached from, written once

## What it is

An environment says how den gets into one network: the bastion to tunnel through, the AWS profile that opens the tunnel, the profile the database credential is minted as, and the region. Services and runbooks name an environment with `env:` instead of repeating those keys, so moving to a new bastion is one edit.

## What you can do

- Reach every data store of a network through one entry: each service names it with `env:`.
- Use two identities: one profile to open the tunnel to the bastion, another for the credential the database accepts.
- Override any key of the environment on one service, for example a different profile for one database.
- Pick another way into the network than SSM for every service of the environment at once, with `transport:`.
- Run a runbook with the environment's profile, region and bastion already exported, by naming it in the runbook's `env:`.
- Mark an environment as production (`environment: prod`, or `production: true`): AI agents then cannot connect its services unless you allow it and confirm each connect.

## Before you start

- The AWS profiles in your AWS config (`~/.aws/config`), and a login for them; `den doctor` checks both.
- For the default SSM transport: an EC2 instance in the network with the SSM agent online, and the profile allowed to start a port-forwarding session on it.

## Configure

An `environments:` map, keyed by the name services use in `env:`.

- `region`: a label you choose for the region, shown in den and handed to runbooks; den does not interpret it.
- `environment`: a label you choose for the stage; `prod` and `production` mark it as production.
- `production`: `true` marks the environment as production whatever its label says.
- `ec2_instance_id`: the SSM-managed bastion. Only the `ssm` transport needs it.
- `aws_profile`: the profile that opens the tunnel.
- `credential_profile`: the profile the database credential is minted as, and the identity the store sees; `aws_profile` when left out.
- `aws_region_code`: the AWS region, such as `eu-central-1`.
- `transport`: how den reaches the network, `ssm` when left out; see Transports.

A development and a production environment, each with its own bastion and profiles:

```yaml
environments:
  dev:
    region: eu
    environment: dev
    ec2_instance_id: i-0123456789abcdef0
    aws_profile: dev-bastion
    credential_profile: dev-data
    aws_region_code: eu-central-1
  prod:
    region: eu
    environment: prod
    ec2_instance_id: i-0fedcba9876543211
    aws_profile: prod-bastion
    credential_profile: prod-readonly
    aws_region_code: eu-central-1
```

One profile for both jobs, when the same identity may open the tunnel and log in to the database:

```yaml
environments:
  sandbox:
    region: eu
    environment: sandbox
    ec2_instance_id: i-0a1b2c3d4e5f60718
    aws_profile: sandbox
    credential_profile: sandbox
    aws_region_code: eu-central-1
```

A service that uses `dev` but mints its token as another profile; any key set on the service wins over the environment's:

```yaml
services:
  - name: payments dev
    type: rds
    env: dev
    rds:
      credential_profile: dev-payments
      rds_host: payments.cluster-abc123.eu-central-1.rds.amazonaws.com
      local_port: 50436
      db_name: payments
      db_user: payments_iam
```

## Limitations

- An environment holds connection settings only: ports, hosts and users stay on each service.
- A service that sets its own `transport:` replaces the environment's whole block; the keys are not merged.
- Production is recognised by `production: true` or an `environment` of `prod` or `production`; another name for a production stage needs the `production: true` key.
- An environment cannot name another one to inherit from.

## When it goes wrong

- `references unknown env`: the service's `env:` names an environment that is not in `environments:`; names are case-sensitive.
- `TargetNotConnected` from SSM: the bastion's SSM agent is offline, or `ec2_instance_id` is in another account or region. `den doctor --deep` asks SSM about every bastion.
- The token is minted but the database rejects it: the database checks `credential_profile` (or `aws_profile` when that is left out), not the profile that opened the tunnel; grant that identity.

## See also

- Transports
- Services
- Runbooks
- AI agents (MCP)
- `docs/transports/README.md`
