# Security policy

## Reporting a vulnerability

Report vulnerabilities **privately** through GitHub private vulnerability reporting:

https://github.com/barzoj-co/llmentary-go/security/advisories/new

Do **not** open a public issue, discussion, or pull request about an unfixed vulnerability.

Proxy (binary / server) vulnerabilities belong in [`barzoj-co/llmentary`](https://github.com/barzoj-co/llmentary/security/advisories/new), not here.

## Supported versions

Only the latest `0.x` minor of this Go module is supported.

## Scope

In scope for this repository:

- Binary download and checksum verification (`DownloadRelease`)
- Handling of `LLMENTARY_*` tokens and environment variables
- The control-API client used from tests

Out of scope:

- The proxy binary and its HTTP surface (report those to `barzoj-co/llmentary`)
- Upstream LLM providers
- Secrets you paste into issues, logs, or traces

## Response

This project has a single maintainer. Reports are handled on a best-effort basis. There is no SLA.
