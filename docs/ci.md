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
  pinned `ProxyVersion` GitHub Release of `barzoj-co/llmentary` (`v0.5.3`)
  with `llmentary.DownloadRelease` (checksum-verified, public
  download). The same step drift-checks `testdata/isolation` against
  the public `codeload.github.com` archive for that `ProxyVersion` tag, not
  proxy `main` and not the rate-limited Contents API. `GITHUB_TOKEN` is
  `${{ github.token }}` only on that install step, as an optional rate-limit
  assist. Runs the mock-upstream example, uploads
  `coverage-integration.out` tokenless, and publishes the same file as a
  workflow artifact for the `test` job gate
- `e2e`: same event filter as `integration`; uses the `openai-e2e` environment
  so each live run waits for maintainer approval; installs the same released
  `v0.5.3` binary (no build from proxy main), downloads and compiles the
  e2e module without secrets, then runs the live OpenAI suite
- `e2e-gate`: always runs. Green when `e2e` succeeded, or when `e2e` was
  skipped on a fork PR (`skipped (fork, no secrets)`). Same-repo PRs still
  fail the gate if live e2e did not run. This is the check to require, not
  `e2e` itself (a skipped required check is treated as passing).
  A rejected or timed-out environment approval makes `e2e-gate` red.

The e2e tests fail (they do not skip) when `OPENAI_API_KEY` is unset so missing
credentials are visible. CI prints the `=== RUN` / `--- PASS` lines so the
executed test names are visible in the log. Secret values are never printed.

## CI secrets and the e2e gate

No secret is set at workflow or job level. `OPENAI_E2E_MODEL` (a variable) may
stay on the `e2e` job. `OPENAI_API_KEY` is only in the step `env:` of
`OpenAI e2e tests`. Presence checks use the boolean form (`secrets.X != ''`),
never `$OPENAI_API_KEY`. The pinned proxy install and isolation-vector
compare may send `Authorization: Bearer $GITHUB_TOKEN` when the install
step sets `GITHUB_TOKEN` from `github.token`. They never use a repo PAT.

`e2e` is not a matrix. Only that job sets `environment: openai-e2e`. Unit,
integration, and mock-upstream tests do not, so they run without approval.
`OPENAI_API_KEY` must not appear on any other job. If an approval is rejected
or times out, `e2e` does not succeed and `e2e-gate` goes red. That is intended.

Fork pull requests skip `e2e` (no secrets). `e2e-gate` then passes with
`skipped (fork, no secrets)`. Same-repo PRs still fail the gate if live e2e
did not run. Never use `pull_request_target` to inject secrets into a fork.

**Known limitation:** on `pull_request`, GitHub runs the workflow and scripts
from the PR's own merge commit, so a PR that edits `ci.yml` or
`scripts/e2e-gate.sh` can make the gate pass. The gate still catches the
normal fork case (e2e skipped, nobody tampered with CI). Tampering is caught
by review of every `.github/**` and `scripts/**` diff. Never "fix" this with
`pull_request_target`.
