# AWS Secrets Manager

Read Secrets Manager secrets as your AWS profile

## What it is

A secret in AWS Secrets Manager, read with the AWS SDK as your profile and shown in the Secrets panel. A JSON secret opens like a file, key by key; any other string as one value. It replaces `aws secretsmanager get-secret-value` and the `jq` after it.

## What you can do

- Open a secret by name or ARN, in any account and region your profile can read.
- Browse a JSON secret's keys as a tree or as dotted paths; a plain string shows as one `value` row and a binary secret as `value (base64)`.
- Reveal and copy a value; a multi-line value such as a PEM key shows on one row and is copied with its line breaks.
- Read the previous version of a secret, or another staging label.
- Keys: press `h` in the Secrets panel.

## Before you start

- No extra tool: den uses the AWS SDK (`den doctor --for aws_secretsmanager`).
- A login for `aws_profile`, from the AWS SSO panel.
- IAM: `secretsmanager:GetSecretValue` on the secret, and `kms:Decrypt` if it is encrypted with a customer-managed KMS key.

## Configure

An entry of `secrets.aws_secretsmanager`; the keys every secret has are in Secrets.

- `secret_id` (required): the secret's name, or its full ARN; use the ARN for a secret in another account.
- `aws_profile`: the profile to read as; the default credential chain without it.
- `aws_region`: the secret's region; the profile's region without it.
- `version_stage` (default `AWSCURRENT`): `AWSPREVIOUS` or a custom staging label.

A JSON secret in your own account:

```yaml
secrets:
  aws_secretsmanager:
    - name: orders api key
      description: client id and private key
      secret_id: /orders/api/key
      aws_profile: dev-data
      aws_region: eu-central-1
```

A secret shared from another account, by ARN:

```yaml
secrets:
  aws_secretsmanager:
    - name: shared signing key
      secret_id: arn:aws:secretsmanager:eu-central-1:210987654321:secret:shared/signing-key-AbCdEf
      aws_profile: dev-data
```

The value before the last rotation, opened flat:

```yaml
secrets:
  aws_secretsmanager:
    - name: orders api key (previous)
      secret_id: /orders/api/key
      aws_profile: dev-data
      version_stage: AWSPREVIOUS
      view: flat
```

## Limitations

- Read-only: den cannot change, rotate or create a secret.
- One secret per entry: there is no listing or search of what an account holds.
- SSM Parameter Store is not supported yet.

## When it goes wrong

- `AccessDeniedException … secretsmanager:GetSecretValue`: the profile may not read this secret.
- `SSO login expired`: log in from the AWS SSO panel; the secret turns ready by itself.
- `ResourceNotFoundException`: the name is wrong, or the secret is in another region or account; set `aws_region`, or use the ARN.

## See also

- Secrets
- AWS SSO sessions
- `docs/secrets/README.md`
