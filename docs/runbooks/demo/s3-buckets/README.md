# S3 buckets

The ten newest S3 buckets of the account the runbook's `env` points at, with their
creation date and age, and how many more there are. A runbook in Go with the AWS SDK
for Go v2, run as `go run .` in its folder: the first run downloads and compiles the
SDK, so its `timeout` is generous.

The SDK reads `AWS_PROFILE` and `AWS_REGION`, which den sets from the `env`, so the
code holds no credentials and no region.

Read-only: `s3:ListAllMyBuckets`. Exits 1 with the reason when the call fails.

The `env` is a placeholder: point `demo-e2e` in `den.yaml` at an account of yours.
