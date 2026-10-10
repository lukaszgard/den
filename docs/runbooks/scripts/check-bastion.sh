#!/usr/bin/env bash
# den:name        Check bastion health
# den:description Verify the SSM bastion for this env is reachable
# den:env         demo-e2e
# den:timeout     30s
# den:param       tier options=test,prod default=test Which tier
#
# A loose script: the comment block with den: lines is all den needs to list it,
# give it a form and run it in the environment it names. m moves it into a folder.
set -euo pipefail
echo "tier ${DEN_PARAM_TIER:-test} · instance ${DEN_EC2_INSTANCE_ID} · ${AWS_REGION} · profile ${AWS_PROFILE}"
echo "bastion reachable"
