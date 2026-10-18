# GitHub Actions and CI

This repository's workflow is `.github/workflows/ci.yml`:

- `lint`: `go vet` and `golangci-lint-action@v8`
- `test`: unit tests for the MIT client (`go test ./...`)
- `e2e`: non-fork only; builds the proxy from
  `barzoj-co/llm-test-lab` source using `secrets.LLMTESTLAB_REPO_TOKEN`, runs the
  mock-upstream example, then runs real OpenAI tests with `secrets.OPENAI_API_KEY`

TODO: switch the e2e job to checksum-verified GitHub Release binaries via
`llmtestlab.DownloadRelease` when `barzoj-co/llm-test-lab` is public.

The e2e tests fail (they do not skip) when `OPENAI_API_KEY` is unset so missing
credentials are visible. CI prints the `=== RUN` / `--- PASS` lines so the
executed test names are visible in the log. Secret values are never printed.
