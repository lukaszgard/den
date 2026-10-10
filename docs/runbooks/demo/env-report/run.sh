#!/usr/bin/env bash
# Prints the connection context den sets for a runbook: what a script gets
# without looking anything up itself.
set -euo pipefail

rows=(
  "AWS_PROFILE|${AWS_PROFILE:-}"
  "AWS_REGION|${AWS_REGION:-}"
  "DEN_ENV|${DEN_ENV:-}"
  "DEN_EC2_INSTANCE_ID|${DEN_EC2_INSTANCE_ID:-}"
  "DEN_RUNBOOK|${DEN_RUNBOOK:-}"
)
for row in "${rows[@]}"; do
  if [[ "${DEN_PARAM_FORMAT:-table}" == "table" ]]; then
    printf '%-22s %s\n' "${row%%|*}" "${row#*|}"
  else
    printf '%s=%s\n' "${row%%|*}" "${row#*|}"
  fi
done
