#!/usr/bin/env bash
# Lists the RDS instances the environment's profile can see, as a table.
# den sets AWS_PROFILE and AWS_REGION from the runbook's env.
set -euo pipefail

rows=$(aws rds describe-db-instances \
  --query 'DBInstances[].[DBInstanceIdentifier,Engine,DBInstanceStatus]' --output text)
count=$(printf '%s\n' "$rows" | grep -c . || true)

echo "$count instances:"
printf '%s\n' "$rows" | column -t
