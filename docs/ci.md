# GitHub Actions and CI

Workflows live under `.github/workflows/`. The main pipeline is `ci.yml`:

- `lint`: `go vet` and a pinned `golangci-lint`
- `test`: unit tests (`go test ./...`) and tokenless Codecov upload (`unit`).
  The job `needs: [integration]` with `if: always()` so it still runs when
  integration is skipped (forks). After unit tests it downloads the
  `coverage-integration` artifact and runs `scripts/coverage-gate.sh`, which
  merges both coverprofiles, applies `codecov.yml` ignores (`examples/**`,
  `e2e/**`), prints the total, and fails below `.coverage-threshold` (or 90
  when the PR head branch is `release-please--*`). Missing integration
  profile fails the required `test` check — same idea as `e2e-gate`.
- `integration`: non-fork pull requests and pushes to `main`; downloads the
  pinned `ProxyVersion` GitHub Release of `barzoj-co/llmentary` (`v0.4.0`)
  with `llmentary.DownloadRelease` (checksum-verified). The existing
  `secrets.LLMTESTLAB_REPO_TOKEN` is passed as `GH_TOKEN` (never in a URL or
  log) so the private release download does not use the default Actions
  `GITHUB_TOKEN`. The same step drift-checks `testdata/isolation` against
  that `ProxyVersion` tag, not proxy `main`. Runs the mock-upstream example, uploads
  `coverage-integration.out` tokenless, and publishes the same file as a
  workflow artifact for the `test` job gate
- `e2e`: same event filter as `integration`; uses the `openai-e2e` environment
  so each live run waits for maintainer approval; installs the same released
  `v0.4.0` binary (no build from proxy main), downloads and compiles the
  e2e module without secrets, then runs the live OpenAI suite
- `e2e-gate`: always runs; green only when `e2e` succeeded. This is the check
  to require, not `e2e` itself (a skipped required check is treated as passing).
  A rejected or timed-out environment approval makes `e2e-gate` red.

The e2e tests fail (they do not skip) when `OPENAI_API_KEY` is unset so missing
credentials are visible. CI prints the `=== RUN` / `--- PASS` lines so the
executed test names are visible in the log. Secret values are never printed.

## CI secrets and the e2e gate

No secret is set at workflow or job level. `OPENAI_E2E_MODEL` (a variable) may
stay on the `e2e` job. `LLMTESTLAB_REPO_TOKEN` is only in the step `env:` of
`Install pinned proxy release`. `OPENAI_API_KEY` is only in the step `env:` of
`OpenAI e2e tests`. Presence checks use the boolean form (`secrets.X != ''`),
never `$OPENAI_API_KEY`.

`e2e` is not a matrix. Only that job sets `environment: openai-e2e`. Unit,
integration, and mock-upstream tests do not, so they run without approval.
`OPENAI_API_KEY` must not appear on any other job. If an approval is rejected
or times out, `e2e` does not succeed and `e2e-gate` goes red. That is intended.

Fork pull requests skip `e2e` (no secrets). `e2e-gate` then fails with
`live e2e did not run; a maintainer must run it`. A maintainer reviews the
diff and pushes it to a same-repo branch; the live job runs there. Never use
`pull_request_target` to "fix" this.

**Known limitation:** on `pull_request`, GitHub runs the workflow and scripts
from the PR's own merge commit, so a PR that edits `ci.yml` or
`scripts/e2e-gate.sh` can make the gate pass. The gate still catches the
normal fork case (e2e skipped, nobody tampered with CI). Tampering is caught
by review of every `.github/**` and `scripts/**` diff. Never "fix" this with
`pull_request_target`.
