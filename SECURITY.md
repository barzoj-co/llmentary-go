# Security policy

## Reporting a vulnerability

Report vulnerabilities **privately** through GitHub private vulnerability reporting:

https://github.com/barzoj-co/llm-test-lab-go/security/advisories/new

Do **not** open a public issue, discussion, or pull request about an unfixed vulnerability.

Private reporting becomes available once this repository is public. Until then, only people with access to the repository can open a draft advisory.

Proxy (binary / server) vulnerabilities belong in [`barzoj-co/llm-test-lab`](https://github.com/barzoj-co/llm-test-lab/security/advisories/new), not here.

## Supported versions

Only the latest `0.x` minor of this Go module is supported.

## Scope

In scope for this repository:

- Binary download and checksum verification (`DownloadRelease`)
- Handling of `LLMTESTLAB_*` tokens and environment variables
- The control-API client used from tests

Out of scope:

- The AGPL proxy binary and its HTTP surface (report those to `barzoj-co/llm-test-lab`)
- Upstream LLM providers
- Secrets you paste into issues, logs, or traces

## Response

This project has a single maintainer. Reports are handled on a best-effort basis. There is no SLA.
