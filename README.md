# llmentary-go

[![CI](https://github.com/barzoj-co/llmentary-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/barzoj-co/llmentary-go/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/barzoj-co/llmentary-go/graph/badge.svg)](https://codecov.io/gh/barzoj-co/llmentary-go)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/barzoj-co/llmentary-go/badge)](https://scorecard.dev/viewer/?uri=github.com/barzoj-co/llmentary-go)
[![License: MIT](https://img.shields.io/github/license/barzoj-co/llmentary-go)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/barzoj-co/llmentary-go.svg)](https://pkg.go.dev/github.com/barzoj-co/llmentary-go)
[![Release](https://img.shields.io/github/v/release/barzoj-co/llmentary-go?sort=semver)](https://github.com/barzoj-co/llmentary-go/releases)

Go client for [llmentary](https://github.com/barzoj-co/llmentary): deterministic
trace-based integration tests for LLM apps. The client never uses an LLM judge.
Assertions run against calls recorded by the local proxy binary.

Renamed from llm-test-lab (pre-release).

This repository is the Go equivalent of `llmentary-python` and
`llmentary-js`.

<!-- llmentary-repos:start -->
## llmentary repositories

| Repository | What |
|---|---|
| [llmentary](https://github.com/barzoj-co/llmentary) | Recording proxy binary (Go) |
| [llmentary-go](https://github.com/barzoj-co/llmentary-go) | Go client for `go test` |
| [llmentary-python](https://github.com/barzoj-co/llmentary-python) | Python client and pytest plugin |
| [llmentary-js](https://github.com/barzoj-co/llmentary-js) | TypeScript/JavaScript client for Vitest and Jest |
<!-- llmentary-repos:end -->

## Install

```bash
go get github.com/barzoj-co/llmentary-go
```

Install the proxy binary from [barzoj-co/llmentary](https://github.com/barzoj-co/llmentary)
(GitHub Releases, or `go install github.com/barzoj-co/llmentary/cmd/llmentary@latest`).
Put it on your `PATH` as `llmentary`, or set `LLMENTARY_BIN`.

## Quickstart

Run the proxy against OpenAI:

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream openai=https://api.openai.com/v1
```

Point `openai-go` at the named upstream prefix and let `llmentary.New(t)` tag
requests:

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
    lab := llmentary.New(t)

    client := openai.NewClient(
        option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
        option.WithBaseURL(lab.OpenAIBaseURL("openai")), // http://127.0.0.1:7777/u/openai
        option.WithHTTPClient(lab.HTTPClient()),         // injects X-Test-Session
        option.WithMaxRetries(0),
    )

    if err := RunWeatherAgent(context.Background(), client, "Weather in Paris?"); err != nil {
        t.Fatal(err)
    }

    assert := lab.Expect(context.Background())
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

If you prefer not to run the proxy as a sidecar, start it with
`llmentary.StartForTest(t, ...)` and pass `proxy.ProxyURL` / `proxy.ControlURL`
to `New` via `WithProxyURL` / `WithControlURL` (see [docs/go-client.md](docs/go-client.md)).

## Binary resolution

The client looks up the proxy executable in this order:

1. `LLMENTARY_BIN` (absolute or relative path)
2. `llmentary` on `PATH` (GitHub Releases / `go install` name)

`DownloadRelease` can fetch a pinned GitHub Release of
`barzoj-co/llmentary` and verify `checksums.txt` before extracting. Releases
may not exist yet while the proxy repository is private.

There is no CGO and no post-install hook.

## License

Licensed under MIT, see LICENSE
