# CloudWatch alarms

Lists the CloudWatch alarms in `ALARM` state in the account the runbook's `env` points
at. A runbook in Rust with the AWS SDK for Rust, run as `cargo run --quiet --release`
in its folder: the first run compiles the SDK, which takes minutes, so its `timeout`
is long. Later runs start at once.

`aws-config` reads `AWS_PROFILE` and `AWS_REGION`, which den sets from the `env`, so
the code holds no credentials and no region.

Read-only: `cloudwatch:DescribeAlarms`.

The exit code is the result: `no alarms firing` exits 0, any alarm in `ALARM` lists it
and exits 1, so `den run "CloudWatch alarms"` can gate a script or a CI step.

The `env` is a placeholder: point `demo-e2e` in `den.yaml` at an account of yours.
