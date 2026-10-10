# Why den?

## What problem does it solve?

Reaching a private database used to be a script and a checklist: log in to AWS SSO with the
right profile, find the bastion, start a port-forward, generate an IAM token that expires in
15 minutes, paste it into `~/.pgpass`, and do it again when the tunnel drops or the token
runs out. With several accounts, regions and stores, the profile names and instance IDs are
the part everybody forgets.

den does those steps for you and keeps doing them. It replaces the checklist, or the shell
script that grew out of it, with one dashboard: the tunnel opens when you press `c`, the
credential is renewed before it expires, and a dropped forward reconnects.

## When den is useful even if you already have…

- **The `aws ssm` CLI.** den runs it for you, the same `AWS-StartPortForwardingSessionToRemoteHost`
  session, and adds what a one-off command lacks: waiting until the port accepts connections,
  minting the credential, refreshing it and reconnecting. The bastion and profiles are written
  once in [`den.yaml`](../environments/README.md) instead of in every command.
- **A GUI database client.** den does not replace it. It opens the tunnel and keeps the
  credential fresh, and `y` copies a connect command for the client you prefer.
- **A VPN.** den can [start one](../vpn/README.md) (`openfortivpn` with SAML) and then
  [reach the stores](../transports/README.md) that sit behind it. The tunnel and the credential
  are still the part den adds.

## What it doesn't try to be

- **Not a SQL client.** It runs yours (`psql`, `mysql`, `redis-cli`, `mongosh`) in a
  [terminal tab](../terminal/README.md) and does not parse or show query results itself.
- **Not a VPN replacement.** It drives `openfortivpn` and SSM, SSH, EC2 Instance Connect or
  `kubectl` tunnels; it does not provide the network.
- **Not an infrastructure tool.** It only calls `Describe` and `List` APIs
  ([`den init`](../init/README.md)) and never creates or changes anything in your account.
  Use Terraform or CloudFormation for that.
- **Not a package manager.** [`den doctor`](../doctor/README.md) prints the install commands
  for the tools den runs; it never runs one.
- **Not complete.** Kafka (Amazon MSK with IAM auth) is planned: a `kafka` service loads, and
  connecting it says so. See [Feature status](../status/README.md).

## See also

- [Security model](../security/README.md): what never leaves den, what it writes to disk and
  what each store needs you to be allowed to do.
