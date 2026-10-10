# OpenSearch

Reaches an **Amazon OpenSearch Service** VPC domain, or an **OpenSearch Serverless**
collection behind a VPC endpoint, through the bastion host. Plain HTTP clients work
against it: `curl`, a browser for OpenSearch Dashboards, scripts, anything.

OpenSearch has no password or token to hand a client. With IAM access control every
request must carry its own SigV4 signature, computed for the real endpoint's hostname.
Through a tunnel the client is talking to `localhost`, so signing tools and TLS
verification both break. den solves this with a **local signing proxy**:

```
curl / browser ──http──▶ den proxy :50161 ──https (SigV4)──▶ SSM tunnel ──▶ domain :443
                         (127.0.0.1 only)
```

For every request the proxy:
- sets `Host` to the real endpoint,
- signs the request as the `credential_profile` identity (credentials refresh on their own),
- sends it over the tunnel with TLS `ServerName` set to the real endpoint, so the
  certificate is **verified**, not skipped.

The proxy listens on `127.0.0.1` only. Anything that can reach it acts with your IAM
identity, so never expose it beyond your machine.

## Configuration

```yaml
services:
  # Managed domain in a VPC
  - name: "EU DEV"
    type: opensearch
    env: eu-dev
    opensearch:
      local_port: 50161
      reconnect: true
      opensearch_host: vpc-logs-int-abc123xyz.eu-central-1.es.amazonaws.com

  # Serverless collection behind a VPC endpoint
  - name: "EU DEV vectors"
    type: opensearch
    env: eu-dev
    opensearch:
      local_port: 50162
      serverless: true
      opensearch_host: abc123xyz.eu-central-1.aoss.amazonaws.com
```

| Field             | Required | Description                                                                  |
|-------------------|----------|------------------------------------------------------------------------------|
| `opensearch_host` | yes      | Domain (`vpc-…es.amazonaws.com`) or collection (`….aoss.amazonaws.com`) endpoint, without `https://` |
| `serverless`      | no       | `true` for a Serverless collection: signs for `aoss` instead of `es`         |
| `local_port`      | no       | Where the proxy listens (default `59200`)                                    |
| `reconnect`       | no       | Auto-reopen the SSM port-forward when it drops (e.g. SSM idle timeout)        |

The `env:` reference supplies the bastion, `aws_profile` (opens the SSM session),
`credential_profile` (**the identity requests are signed as**) and `aws_region_code`. The
SSM forward itself lands on a random private port that only the proxy uses.

## Prerequisites

- **AWS CLI v2** and the **Session Manager plugin**; a valid SSO session for both profiles.
  den checks the signing identity's credentials on connect, so an expired session
  fails there instead of on every request.
- **Network:** the bastion must reach the endpoint on 443 (security group of the domain
  or of the collection's VPC endpoint).
- **Access:**
  - Managed domain: the domain access policy allows the signing role (`es:ESHttp*`).
    With fine-grained access control, also map the role to an OpenSearch role.
  - Serverless: a data access policy granting the signing role access to the collection
    and its indexes, and a network policy that allows the VPC endpoint.

## Usage

1. Launch `den`, open **Connect**, select the OpenSearch service and press `c`. den opens
   the tunnel, verifies the port, checks credentials, then starts the proxy.
2. Press `y` to copy the command:
   ```bash
   curl -s 'http://localhost:50161/_cluster/health?pretty'     # managed domain
   curl -s 'http://localhost:50162/_cat/indices?v'             # Serverless
   ```
   Any path works the same way, for example
   `curl -s -XPOST localhost:50161/logs-*/_search -H 'Content-Type: application/json' -d '{"size":1}'`.
3. **Dashboards:** open `http://localhost:50161/_dashboards` in a browser. The browser
   needs no AWS login, because the proxy signs every request. If the domain puts
   Dashboards behind Cognito or SAML instead of IAM, only the REST API works this way.
4. **A shell inside den:** in the detail view, `t` opens your shell in a
   [terminal tab](../terminal/README.md#shell-tabs) next to the logs, with `DEN_URL`
   set to the proxy: `curl -s "$DEN_URL/_cat/indices?v"`.
5. `d` disconnects and stops the proxy, and the shells in the service's terminal tabs.

Requests that fail upstream (status ≥ 400) are logged in the detail view, which is
usually the fastest way to spot a missing permission.

## Troubleshooting

- **`403 … no permissions for [indices:data/read/search]`:** signing worked. Fine-grained
  access control has not mapped the role, or the Serverless data access policy does not
  cover the index.
- **`403 User: … is not authorized to perform: es:ESHttpGet`:** the domain access policy
  does not allow the signing role.
- **`502 den signing proxy: … certificate`:** the tunnel reached something that is not the
  endpoint in `opensearch_host`, often a custom domain endpoint. Set `opensearch_host` to
  the name on the certificate.
- **Browser redirected away from localhost:** absolute redirects to the endpoint are
  rewritten back to the proxy. Please report any that are not.
