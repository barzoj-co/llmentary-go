# GitHub Actions and CI

Workflows live under `.github/workflows/`. The main pipeline is `ci.yml`:

- `lint`: `go vet` and a pinned `golangci-lint`
- `test`: unit tests (`go test ./...`) and tokenless Codecov upload (`unit`)
- `integration`: non-fork pull requests and pushes to `main`; builds the proxy
  from `barzoj-co/llm-test-lab` with `secrets.LLMTESTLAB_REPO_TOKEN`, runs the
  mock-upstream example, uploads `coverage-integration.out` tokenless
- `e2e`: same event filter as `integration`; builds the proxy, downloads and
  compiles the e2e module without secrets, then runs the live OpenAI suite
- `e2e-gate`: always runs; green only when `e2e` succeeded. This is the check
  to require, not `e2e` itself (a skipped required check is treated as passing)

TODO: switch the proxy checkout to checksum-verified GitHub Release binaries via
`llmtestlab.DownloadRelease` when `barzoj-co/llm-test-lab` is public.

The e2e tests fail (they do not skip) when `OPENAI_API_KEY` is unset so missing
credentials are visible. CI prints the `=== RUN` / `--- PASS` lines so the
executed test names are visible in the log. Secret values are never printed.

## CI secrets and the e2e gate

No secret is set at workflow or job level. `OPENAI_E2E_MODEL` (a variable) may
stay on the `e2e` job. `LLMTESTLAB_REPO_TOKEN` is only passed as
`with: token:` on the `actions/checkout` step for `barzoj-co/llm-test-lab`.
`OPENAI_API_KEY` is only in the step `env:` of `OpenAI e2e tests`. Presence
checks use the boolean form (`secrets.X != ''`), never `$OPENAI_API_KEY`.

`e2e` is not a matrix. To require maintainer approval for live OpenAI calls,
add one line directly above `runs-on:` after the environment exists:

```yaml
#   environment: openai-e2e
```

Do not add `environment:` until that environment is created with protection
rules and `OPENAI_API_KEY` is moved into it (referencing a name auto-creates an
unprotected environment). If an approval is later rejected or times out, `e2e`
does not succeed and `e2e-gate` goes red. That is intended.

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
