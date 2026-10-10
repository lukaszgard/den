# den init

## What it is

`den init` writes a starter `den.yaml` from what one AWS profile can see in one
region, so a new user does not have to look up instance IDs and endpoints by
hand. It lists:

- **bastion candidates** — SSM-managed EC2 instances, online ones first
- **data stores** — RDS and Aurora (PostgreSQL, MySQL/MariaDB), ElastiCache
  (replication groups and Serverless), MemoryDB, DocumentDB, Redshift
  (provisioned and Serverless), OpenSearch VPC domains and Neptune

and writes one environment (the bastion, profile and region) plus one service per
data store, each with its own local port.

It only reads — `Describe*` and `List*` calls — and the result is an ordinary,
static config: den never discovers anything at run time.

What it cannot know is written as a comment for you to fill in:

- the database user you connect as (`db_user`; required for MySQL)
- your ElastiCache/MemoryDB user for IAM auth (`cache_name`, `user_id`)
- stores with IAM database authentication **off** are written commented out,
  with the reason — den cannot mint a token for them until it is switched on
- caveats such as a Redis cluster without in-transit encryption, or a
  multi-shard cluster that one tunnel cannot fully reach

## Configuration

The output is the configuration. Flags:

| Flag | Default | Meaning |
|---|---|---|
| `--profile` | `$AWS_PROFILE` | AWS profile to look with; also written into the config |
| `--region` | the profile's region | where to look |
| `--bastion` | the only online instance, or a picker | instance ID to tunnel through |
| `--env` | the region | name of the generated environment |
| `-o`, `--output` | `-` (stdout) | file to write |
| `--force` | off | overwrite an existing output file |

## Prerequisites

- A valid login for the profile (`aws sso login --profile …`)
- Read permissions: `ssm:DescribeInstanceInformation`, `rds:DescribeDBClusters`,
  `rds:DescribeDBInstances`, `elasticache:DescribeReplicationGroups`,
  `elasticache:DescribeServerlessCaches`, `memorydb:DescribeClusters`,
  `redshift:DescribeClusters`, `redshift-serverless:ListWorkgroups`,
  `es:ListDomainNames`, `es:DescribeDomains`. A missing one skips that source
  with a note; only the SSM listing is essential.

## Usage

```bash
den init --profile dev                          # print to stdout
den init --profile dev -o ~/.config/den/den.yaml
den init --profile dev --region us-east-1 --env us-dev -o us.yaml
den doctor -c ~/.config/den/den.yaml             # then check the result
```

With several online instances and a terminal, den asks which to use; without a
terminal it lists them and asks for `--bastion`.
