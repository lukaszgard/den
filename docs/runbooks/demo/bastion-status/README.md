# Bastion status

Is the environment's bastion running, and does it answer over SSM, the way den's
tunnels need it to? A runbook in Python with boto3, run as `uv run --quiet main.py`:
uv reads the dependency from the header of `main.py` and builds the environment on
the first run.

It reads the bastion's instance id from `DEN_EC2_INSTANCE_ID`, which den sets from the
runbook's `env`, and asks EC2 for its state and SSM for its ping status.

Read-only: `ec2:DescribeInstances` and `ssm:DescribeInstanceInformation`.

A good result prints `bastion is reachable` and exits 0. A stopped instance, or one
whose SSM agent is not `Online`, exits 1.

The `env` is a placeholder: point `demo-e2e` in `den.yaml` at an account of yours.
