---
title: den
layout: hextra-home
---

{{< hextra/hero-badge link="docs/changelog/" >}}
  What's new in den
{{< /hextra/hero-badge >}}

{{< hextra/hero-headline style="margin-top: 1.5rem; margin-bottom: 1.5rem" >}}
  Your private cloud data stores, from one terminal
{{< /hextra/hero-headline >}}

{{< hextra/hero-subtitle style="margin-bottom: 1.5rem" >}}
  den opens the tunnel, mints and refreshes the IAM credential and hands you a ready connect command for RDS, ElastiCache, MemoryDB, DocumentDB, Redshift, OpenSearch and Neptune.
{{< /hextra/hero-subtitle >}}

{{< tabs >}}
  {{< tab name="macOS / Linux" >}}
```sh
curl -fsSL https://github.com/lukaszgard/den/releases/latest/download/install.sh | sh
```
  {{< /tab >}}
  {{< tab name="Windows" >}}
```powershell
irm https://github.com/lukaszgard/den/releases/latest/download/install.ps1 | iex
```
  {{< /tab >}}
{{< /tabs >}}

{{< hextra/hero-button text="Install den" link="docs/install/" style="margin-bottom: 2rem" >}}
{{< hextra/hero-button text="Get started" link="docs/quickstart/" style="margin-left: 0.5rem; margin-bottom: 2rem; --den-outline: 1; background: transparent; border: 1px solid currentColor" >}}
{{< hextra/hero-button text="Install what den needs" link="docs/doctor/install/" style="margin-left: 0.5rem; margin-bottom: 2rem; --den-outline: 1; background: transparent; border: 1px solid currentColor" >}}

{{< hextra/hero-subtitle style="margin-top: 2rem; margin-bottom: 1rem" >}}
  What's new
{{< /hextra/hero-subtitle >}}

{{< den/whats-new >}}

{{< hextra/feature-grid style="margin-top: 2rem" >}}
  {{< hextra/feature-card title="Every data store, one tunnel" icon="database" link="docs/rds/" subtitle="RDS and Aurora, ElastiCache, MemoryDB, DocumentDB, Redshift, OpenSearch and Neptune: den opens the forward, mints the IAM credential, refreshes it and reconnects when it drops." >}}
  {{< hextra/feature-card title="Transports" icon="switch-horizontal" link="docs/transports/" subtitle="Reach the network through SSM (the default), an SSH bastion, an EC2 Instance Connect Endpoint or kubectl port-forward." >}}
  {{< hextra/feature-card title="AWS SSO" icon="cloud" link="docs/aws/" subtitle="One login entry per SSO session, named and curated from den.yaml." >}}
  {{< hextra/feature-card title="VPN and tunnels" icon="lock-closed" link="docs/vpn/" subtitle="openfortivpn with SAML, and SSH/SOCKS tunnels with a real data-path health check." >}}
  {{< hextra/feature-card title="Terminal tabs" icon="terminal" link="docs/terminal/" subtitle="Run psql, mysql, redis-cli or mongosh inside den, next to the logs and already logged in." >}}
  {{< hextra/feature-card title="Secrets" icon="key" link="docs/secrets/" subtitle="Search and copy secrets from SOPS, AWS Secrets Manager, KeePassXC and Vault; masked until revealed, never written to disk." >}}
  {{< hextra/feature-card title="Runbooks" icon="document-text" link="docs/runbooks/" subtitle="Your scripts, one folder each, run with den's context: AWS profile, region and the live tunnel ports. Share them from git." >}}
  {{< hextra/feature-card title="AI agents" icon="sparkles" link="docs/mcp/" subtitle="den mcp lets Claude Code or Cursor list services and open tunnels without ever seeing a credential." >}}
  {{< hextra/feature-card title="den doctor" icon="shield-check" link="docs/doctor/" subtitle="Checks the tools your den.yaml needs and prints the install commands for your OS." >}}
{{< /hextra/feature-grid >}}
