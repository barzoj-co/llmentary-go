# Contributing

Thanks for working on `llm-test-lab-go`.

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md). To report a vulnerability
in this client, see [SECURITY.md](SECURITY.md). Do not open a public issue for
either.

## Requirements

- Go 1.22 or newer (CI uses 1.26.x to match the proxy).
- The proxy from [barzoj-co/llm-test-lab](https://github.com/barzoj-co/llm-test-lab)
  for integration and e2e tests.

## Local checks

Unit tests (no proxy binary):

```bash
go test ./...
```

Integration example (needs `llm-test-lab` on PATH or `LLMTESTLAB_BIN`):

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
# Requires actionlint and shellcheck on PATH
actionlint

./scripts/check-pinned-actions.sh
./scripts/check-pinned-actions.sh --self-test
./scripts/check-workflow-policy.sh
./scripts/check-readme.sh
./scripts/check-mit-docs.sh
```

Install `actionlint` from the [rhysd/actionlint releases](https://github.com/rhysd/actionlint/releases)
(CI pins the version in `.github/workflows/hygiene.yml`).

## Layout

- Root module `github.com/barzoj-co/llm-test-lab-go`, package `llmtestlab`
- `examples/go` — proxy-backed example against a mock upstream
- `e2e` — real OpenAI Chat Completions and Responses flows
- `docs/go-client.md` — assertion and session API

The client talks to the proxy over HTTP only. Do not import proxy/server packages
from `barzoj-co/llm-test-lab`.

## Pull requests

Keep changes focused. Never merge your own PR unless a maintainer asks you to.

PR titles must be [conventional commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `docs:`, `chore:`, `ci:`, `refactor:`, `test:`) so
[release-please](https://github.com/googleapis/release-please) can open the
changelog PR. Tags are plain `vX.Y.Z` on the root module; `examples/go` and `e2e`
are not tagged.

Coverage is **informational**. Codecov comments and status checks must not fail a
PR (project and patch are `informational: true`).

## Renovate

Dependency updates come from Renovate, not Dependabot version PRs.

- Safe updates (patch, pin, digest, and GitHub Actions non-major) automerge when
  CI is green.
- The root module `go 1.22` directive is the users' minimum Go version and is
  not bumped by Renovate. The CI Go version in `setup-go` may be.
- `examples/go` and `e2e` are test-only modules; non-major updates automerge.
- Security advisories can open outside the Monday morning schedule.
