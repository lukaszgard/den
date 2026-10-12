# Feature status

What is settled and what is still moving. den is below 1.0, so its keys and commands can
change between minor versions; a change that breaks an existing `den.yaml` or command gets a
**Breaking:** line at the top of its [changelog](../../CHANGELOG.md) entry, with the migration.

| Status | Means |
|---|---|
| Stable | In regular use and documented. A change that breaks it comes with a **Breaking:** line in the changelog. |
| Beta | Works and is documented, but is newer and has seen less use. Its keys and behaviour may still change. |
| Planned | Not implemented. |

## Beta

| Feature | Page |
|---|---|
| Writing and changing a runbook with an agent: `den runbook new --agent`, `n` and `E` in the Runbooks panel, the `agent:` key and `agents:` entries, drafts and saved requests (`runbooks.drafts`) | [CLI](../runbooks/agents/cli/README.md), [panel](../runbooks/agents/panel/README.md), [MCP](../runbooks/agents/mcp/README.md), [drafts](../runbooks/agents/drafts/README.md) |
| Runbook sources: runbooks from a git repository, `den.lock`, trust | [Runbook sources](../runbook-sources/README.md) |
| Terminal tabs: the data store's client inside den | [Terminal tabs](../terminal/README.md) |

## Planned

| Feature | State |
|---|---|
| Kafka (Amazon MSK with IAM auth) | A `kafka` service loads and validates, and connecting it reports that Kafka is not implemented yet. |

## Stable

Everything else: the Connect, Tunnel, VPN, AWS, Secrets, Runbooks, Config, Logs, Help and Updates
panels; [RDS](../rds/README.md), [ElastiCache](../elasticache/README.md),
[MemoryDB](../memorydb/README.md), [DocumentDB](../documentdb/README.md),
[Redshift](../redshift/README.md), [OpenSearch](../opensearch/README.md) and
[Neptune](../neptune/README.md); [transports](../transports/README.md); SOPS, AWS Secrets Manager,
KeePassXC and Vault secrets; [runbooks](../runbooks/README.md), sequences and `den run`;
[den mcp](../mcp/README.md); [den doctor](../doctor/README.md), [den init](../init/README.md) and the
[JSON Schema](../schema/README.md).

## Known gaps

- **Updating den from inside den on Windows is untested.** den replaces its binary by renaming a new one
  over it, which Windows may refuse while `den.exe` runs, and the restart after an update is offered on
  macOS and Linux only. On Windows, install a new version with `install.ps1`
  ([Install](../install/README.md)).
- **The VPN needs a kernel with PPP.** WSL 3.0.1's kernel has none; see
  [Troubleshooting](../troubleshooting/README.md#the-kernel-has-no-ppp-wsl).
- **Planned secret providers** (AWS SSM Parameter Store and a generic `command` provider) are in the
  commented examples of [`den.yaml`](../../den.yaml), not implemented: using one stops den loading the file.
