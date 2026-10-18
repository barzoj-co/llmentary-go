# llm-test-lab-go

MIT Go client for [llm-test-lab](https://github.com/barzoj-co/llm-test-lab):
deterministic, trace-based integration tests for LLM apps. The client never uses
an LLM judge. Assertions run against calls recorded by the local proxy binary.

This repository is the Go equivalent of `llm-test-lab-python` and
`llm-test-lab-js`: a small MIT library that talks to the AGPL proxy.

## Install

```bash
go get github.com/barzoj-co/llm-test-lab-go
```

Install the proxy binary from [barzoj-co/llm-test-lab](https://github.com/barzoj-co/llm-test-lab)
(GitHub Releases, or `go install github.com/barzoj-co/llm-test-lab/cmd/llmtestlab@latest`).
Put it on your `PATH` as `llm-test-lab`, or set `LLMTESTLAB_BIN`.

## Quickstart

Run the proxy against OpenAI:

```bash
llm-test-lab \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream openai=https://api.openai.com/v1
```

Point `openai-go` at the named upstream prefix and let `llmtestlab.New(t)` tag
requests:

```go
package myapp_test

import (
    "context"
    "os"
    "testing"

    "github.com/barzoj-co/llm-test-lab-go"
    "github.com/openai/openai-go"
    "github.com/openai/openai-go/option"
)

func TestWeatherTool(t *testing.T) {
    lab := llmtestlab.New(t)

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
    assert.ToolCalled("get_weather", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
    assert.NoRetries()
    assert.NoErrors()
    assert.TokensBelow(1000)
}
```

```bash
OPENAI_API_KEY=sk-... go test ./...
```

If you prefer not to run the proxy as a sidecar, start it with
`llmtestlab.StartForTest(t, ...)` and pass `proxy.ProxyURL` / `proxy.ControlURL`
to `New` via `WithProxyURL` / `WithControlURL` (see [docs/go-client.md](docs/go-client.md)).

## Binary resolution

The client looks up the proxy executable in this order:

1. `LLMTESTLAB_BIN` (absolute or relative path)
2. `llm-test-lab` on `PATH`
3. `llmtestlab` on `PATH` (GoReleaser / `go install` name)

`DownloadRelease` can fetch a pinned GitHub Release of
`barzoj-co/llm-test-lab` and verify `checksums.txt` before extracting. Releases
may not exist yet while the proxy repository is private.

There is no CGO and no post-install hook.

## License

MIT. The proxy binary in [barzoj-co/llm-test-lab](https://github.com/barzoj-co/llm-test-lab)
is AGPL-3.0; using it from tests does not change your application's license.
