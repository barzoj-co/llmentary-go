# llmentary-go

Go client for deterministic, trace-based LLM integration tests.

[![CI](https://github.com/barzoj-co/llmentary-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/barzoj-co/llmentary-go/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/barzoj-co/llmentary-go/graph/badge.svg)](https://codecov.io/gh/barzoj-co/llmentary-go)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/barzoj-co/llmentary-go/badge)](https://scorecard.dev/viewer/?uri=github.com/barzoj-co/llmentary-go)
[![License: MIT](https://img.shields.io/github/license/barzoj-co/llmentary-go)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/barzoj-co/llmentary-go.svg)](https://pkg.go.dev/github.com/barzoj-co/llmentary-go)
[![Release](https://img.shields.io/github/v/release/barzoj-co/llmentary-go?sort=semver)](https://github.com/barzoj-co/llmentary-go/releases)

llmentary-go is the Go client for [llmentary](https://github.com/barzoj-co/llmentary).
Point an OpenAI-compatible SDK at the local recording proxy, then assert on the
recorded trace: tool names, arguments, order, retries, token usage, latency, and
provider errors. The client never uses an LLM judge.

## Install

```bash
go get github.com/barzoj-co/llmentary-go
```

Install the proxy from [barzoj-co/llmentary](https://github.com/barzoj-co/llmentary)
GitHub Releases, or `go install github.com/barzoj-co/llmentary/cmd/llmentary@latest`.
Put it on your `PATH` as `llmentary`, or set `LLMENTARY_BIN`. Lookup details:
[docs/binaries.md](docs/binaries.md).

## Quickstart

Run the proxy against OpenAI, then point `openai-go` at the named upstream.
`llmentary.New(t)` tags requests with `X-Test-Session`:

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream openai=https://api.openai.com/v1
```

```go
package myapp_test

import (
    "context"
    "os"
    "testing"

    "github.com/barzoj-co/llmentary-go"
    "github.com/openai/openai-go"
    "github.com/openai/openai-go/option"
)

func TestWeatherTool(t *testing.T) {
    lm := llmentary.New(t)

    client := openai.NewClient(
        option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
        option.WithBaseURL(lm.OpenAIBaseURL("openai")), // http://127.0.0.1:7777/u/openai
        option.WithHTTPClient(lm.HTTPClient()),         // injects X-Test-Session
        option.WithMaxRetries(0),
    )

    _, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
        Model: "gpt-4.1-mini",
        Messages: []openai.ChatCompletionMessageParamUnion{
            openai.UserMessage("Weather in Paris?"),
        },
    })
    if err != nil {
        t.Fatal(err)
    }

    assert := lm.Expect(context.Background())
    assert.CallCount(1)
    assert.ToolCalled("get_weather", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
    assert.NoRetries()
    assert.NoErrors()
    assert.TokensBelow(1000)
}
```

```bash
OPENAI_API_KEY=sk-... go test ./...
```

The same API is exercised without a live provider in
[`examples/go/weather_test.go`](examples/go/weather_test.go), which starts the
proxy from the test with `StartForTest`.

## Assertions

`lm.Expect(ctx)` waits until the session is quiet, then fails with `t.Fatal`.
`lm.Check(ctx)` is the same API with `t.Error`. Filter recorded calls with
`trace.Query()` before `Expect(t)` / `Check(t)`.

```go
assert := lm.Expect(context.Background())
assert.CallCount(1)
assert.ToolCalled("get_weather", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
assert.ToolNotCalled("delete_user")
assert.NoRetries()
assert.NoErrors()
assert.TokensBelow(1000)
```

See [docs/go-client.md](docs/go-client.md) for the full session, query, and
assertion API.

<!-- llmentary-repos:start -->
## llmentary repositories

| Repository | What |
|---|---|
| [llmentary](https://github.com/barzoj-co/llmentary) | Recording proxy binary (Go) |
| [llmentary-go](https://github.com/barzoj-co/llmentary-go) | Go client for `go test` |
| [llmentary-python](https://github.com/barzoj-co/llmentary-python) | Python client and pytest plugin |
| [llmentary-js](https://github.com/barzoj-co/llmentary-js) | TypeScript/JavaScript client for Vitest and Jest |
<!-- llmentary-repos:end -->

## License

Licensed under MIT, see LICENSE
