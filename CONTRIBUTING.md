# Contributing

Thanks for working on `llmentary-go`.

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md). To report a vulnerability
in this client, see [SECURITY.md](SECURITY.md). Do not open a public issue for
either.

## Requirements

- Go 1.22 or newer (CI uses 1.26.10 to match the proxy).
- The proxy from [barzoj-co/llmentary](https://github.com/barzoj-co/llmentary)
  for integration and e2e tests.

## Local checks

Unit tests (no proxy binary):

```bash
go test ./...
```

Integration example (needs `llmentary` on PATH or `LLMENTARY_BIN`):

```bash
(cd examples/go && go test ./...)
```

Real OpenAI e2e (must actually run when `OPENAI_API_KEY` is set; they fail rather
than skip if the key is missing):

```bash
(cd e2e && OPENAI_API_KEY=... go test -v -tags=e2e ./...)
```

Never print secret values in logs or test output.

Before sending code changes:

```bash
gofmt -w .
go vet ./...
```

CI also runs `golangci-lint` (pinned version, not `latest`).

Workflow and docs hygiene (run from the repository root):

```bash
# Requires actionlint, shellcheck, python3+PyYAML, and gitleaks on PATH
actionlint

./scripts/check-pinned-actions.sh
./scripts/check-pinned-actions.sh --self-test
./scripts/check-workflow-policy.sh
./scripts/check-secret-scope.sh
./scripts/check-secret-scope.sh --self-test
./scripts/check-pr-oidc.sh
./scripts/check-pr-oidc.sh --self-test
./scripts/e2e-gate.sh --self-test
./scripts/check-gitleaks-config.sh --self-test
./scripts/check-readme.sh
./scripts/check-mit-docs.sh
```

Install `actionlint` from the [rhysd/actionlint releases](https://github.com/rhysd/actionlint/releases)
(CI pins the version in `.github/workflows/hygiene.yml`).

## Layout

- Root module `github.com/barzoj-co/llmentary-go`, package `llmentary`
- `examples/go` — proxy-backed example against a mock upstream
- `e2e` — real OpenAI Chat Completions and Responses flows
- `docs/go-client.md` — assertion and session API
- `docs/binaries.md` — proxy binary lookup and `DownloadRelease`

The client talks to the proxy over HTTP only. Do not import proxy/server packages
from `barzoj-co/llmentary`.

## Pull requests

Keep changes focused. Never merge your own PR unless a maintainer asks you to.

PR titles must be [conventional commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `docs:`, `chore:`, `ci:`, `refactor:`, `test:`) so
[release-please](https://github.com/googleapis/release-please) can open the
changelog PR. Tags are plain `vX.Y.Z` on the root module (`include-component-in-tag`
is false); `examples/go` and `e2e` are not tagged. The first release is forced to
`0.1.0` via `initial-version` in `release-please-config.json` (Go modules stay on
the v0 import path). That key only applies when no prior tag exists and can stay
after `v0.1.0`.

Coverage is **informational**. Codecov comments and status checks must not fail a
PR (project and patch are `informational: true`).

## CI secrets and the e2e gate

Secrets stay off workflow- and job-level `env:`. `OPENAI_API_KEY` is injected
only into the live `OpenAI e2e tests` step; `LLMTESTLAB_REPO_TOKEN` is only in
the `Install pinned proxy release` step (passed as `GH_TOKEN` for private
GitHub Release assets; never printed or placed in a URL). The
live `e2e` job uses the `openai-e2e` GitHub Actions environment. Maintainers
approve that environment
deployment on each pull request (and on `push` to `main`) before the paid
OpenAI suite runs. Integration and mock-upstream tests do not use the
environment and do not wait for approval. Fork PRs skip `e2e`; `e2e-gate`
fails until a maintainer pushes the same commits to a same-repo branch. See
[docs/ci.md](docs/ci.md) for the known `pull_request` merge-commit limitation.
Never use `pull_request_target`.

## Renovate

Dependency updates come from Renovate, not Dependabot version PRs.

- Safe updates (patch, pin, digest, and GitHub Actions non-major) automerge when
  CI is green.
- The root module `go 1.22` directive is the users' minimum Go version and is
  not bumped by Renovate. The CI Go version in `setup-go` may be.
- `examples/go` and `e2e` are test-only modules; non-major updates automerge.
- Security advisories can open outside the Monday morning schedule.
