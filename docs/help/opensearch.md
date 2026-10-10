# OpenSearch

Domains and Serverless, signed on localhost

## What it is

An Amazon OpenSearch Service VPC domain, or an OpenSearch Serverless collection behind a VPC endpoint, reached through a tunnel. With IAM access control every request must carry a signature made for the real endpoint, which a client talking to `localhost` cannot make. So den runs a signing proxy on `127.0.0.1`: `curl`, scripts and your browser talk plain HTTP to it, and it signs each request as you and sends it over the tunnel with TLS verified.

## What you can do

- Query a domain or collection with plain `curl` on `localhost`, no signing tool needed.
- Open OpenSearch Dashboards in your browser at `http://localhost:<port>/_dashboards`, without an AWS login in the browser.
- Open your own shell in a terminal tab inside den, with `DEN_URL` set to the proxy.
- See requests that failed upstream (status 400 or more) in the detail view's log, the quickest way to spot a missing permission.
- Keys: press `h` in the detail view.

## Before you start

- `curl`, or any HTTP client (`den doctor --for opensearch`).
- A working AWS login for `credential_profile`: requests are signed as that identity, and den checks it on connect.
- A domain: an access policy allowing that role (`es:ESHttp*`), and with fine-grained access control, the role mapped to an OpenSearch role.
- A Serverless collection: a data access policy granting the role the collection and its indexes, and a network policy that allows the VPC endpoint.
- The bastion must reach the endpoint on port 443.

## Configure

A service of `type: opensearch` with an `opensearch:` section; the shared keys are in Services.

- `opensearch_host` (required): the domain or collection endpoint, without `https://`.
- `serverless` (default `false`): `true` for a Serverless collection, signed for `aoss` instead of `es`.
- `local_port` (default `59200`): where the proxy listens on `127.0.0.1`.

A domain in a VPC:

```yaml
services:
  - name: logs dev
    type: opensearch
    env: dev
    opensearch:
      opensearch_host: vpc-logs-dev-abc123.eu-central-1.es.amazonaws.com
      local_port: 59200
```

A Serverless collection, reopened after a drop:

```yaml
services:
  - name: vectors dev
    type: opensearch
    env: dev
    opensearch:
      opensearch_host: abc123xyz.eu-central-1.aoss.amazonaws.com
      serverless: true
      local_port: 59203
      reconnect: true
```

## Limitations

- Anything that can reach the proxy acts as you: it listens on `127.0.0.1` only, and must never be exposed further.
- Dashboards behind Cognito or SAML instead of IAM do not work through the proxy; the REST API still does.
- `opensearch_host` must be the name on the endpoint's certificate: a custom domain endpoint needs its own name there.
- The shell tab gets no credential: only requests through `$DEN_URL` are signed.

## When it goes wrong

- `403 … no permissions for [indices:data/read/search]`: signing worked; fine-grained access control has not mapped the role, or the Serverless data access policy does not cover the index.
- `403 User: … is not authorized to perform: es:ESHttpGet`: the domain access policy does not allow the role.
- `502 den signing proxy: … certificate`: the tunnel reached something other than `opensearch_host`, often a custom endpoint; use the name on the certificate.

## See also

- Neptune
- Services
- Terminal tabs
- `docs/opensearch/README.md`
