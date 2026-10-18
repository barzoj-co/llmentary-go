# Contributing

Thanks for working on `llm-test-lab-go`.

## Requirements

- Go 1.22 or newer (CI uses 1.26.x to match the proxy).
- The AGPL proxy from [barzoj-co/llm-test-lab](https://github.com/barzoj-co/llm-test-lab)
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

CI also runs `golangci-lint-action@v8`.

## Layout

- Root module `github.com/barzoj-co/llm-test-lab-go`, package `llmtestlab`
- `examples/go` — proxy-backed example against a mock upstream
- `e2e` — real OpenAI Chat Completions and Responses flows
- `docs/go-client.md` — assertion and session API

The client talks to the proxy over HTTP only. Do not import proxy/server packages
from `barzoj-co/llm-test-lab`.

## Pull requests

Keep changes focused. Never merge your own PR unless a maintainer asks you to.
