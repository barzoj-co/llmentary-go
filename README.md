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
GitHub Releases (`v0.3.0`), or `go install github.com/barzoj-co/llmentary/cmd/llmentary@v0.3.0`.
Put it on your `PATH` as `llmentary`, or set `LLMENTARY_BIN`. Lookup details:
[docs/binaries.md](docs/binaries.md).

## Quickstart

Run the proxy (no `--upstream` needed: it defaults to
`openai=https://api.openai.com/v1`). `llmentary.New(t)` tags requests with
`X-Test-Session`:

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778
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
        option.WithBaseURL(lm.OpenAIBaseURL("")), // http://127.0.0.1:7777/v1
        option.WithHTTPClient(lm.HTTPClient()),         // injects X-Test-Session
        option.WithMaxRetries(0),
    )

    _, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
        Model: "gpt-4.1-mini",
        Messages: []openai.ChatCompletionMessageParamUnion{
            openai.UserMessage("Weather in Paris?"),
        },
        Tools: []openai.ChatCompletionToolParam{{
            Function: openai.FunctionDefinitionParam{
                Name: "get_weather",
                Parameters: openai.FunctionParameters{
                    "type": "object",
                    "properties": map[string]any{
                        "city": map[string]any{"type": "string"},
                    },
                    "required": []string{"city"},
                },
            },
        }},
        ToolChoice: openai.ChatCompletionToolChoiceOptionParamOfChatCompletionNamedToolChoice(
            openai.ChatCompletionNamedToolChoiceFunctionParam{Name: "get_weather"},
        ),
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

Service under test cannot send headers (import-time / env-only clients). Set
the SDK base URL to `lm.PathBaseURL("")` **before** the app is constructed:

```go
func TestService(t *testing.T) {
    lm := llmentary.New(t)
    t.Setenv("OPENAI_BASE_URL", lm.PathBaseURL("")) // http://127.0.0.1:7777/s/<id>/v1
    // construct or import the service after this line
}
```

Black-box and sidecar recipes:
[docs/black-box.md](https://github.com/barzoj-co/llmentary/blob/main/docs/black-box.md).
When to assert on the trace vs the model's wording:
[docs/assertions-guide.md](https://github.com/barzoj-co/llmentary/blob/main/docs/assertions-guide.md).
Control API contract:
[api/control/openapi.yaml](https://github.com/barzoj-co/llmentary/blob/main/api/control/openapi.yaml).

The same API is exercised without a live provider in
[`examples/go/weather_test.go`](examples/go/weather_test.go), which starts the
proxy from the test with `StartForTest`.

## Assertions

`lm.Expect(ctx)` waits until the session is quiet, then fails with `t.Fatal`.
`lm.Check(ctx)` is the same API with `t.Error`. Filter recorded calls with
`trace.Query()` before `Expect(t)` / `Check(t)`.

```go
mark := lm.Mark(context.Background())
// … one turn …
assert := lm.Expect(context.Background(), llmentary.Since(mark))
assert.CallCount(1)
assert.ToolCalled("get_weather", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
assert.ToolNotCalled("delete_user")
assert.NoRetries()
assert.NoErrors()
assert.TokensBelow(1000)
```

`NoErrors` / `ToolNotCalled` fail when a filter or `Since` matches zero calls.
Use `llmentary.AllowEmpty()` only when the test intends an empty match.

`Call.ID` is unique across sessions in one proxy process; `Call.Seq` is
1-based within the session. `Summary()` prints `#<seq> (<id>)`.

Prove two path-prefix sessions stayed apart with
`lm.Expect(ctx).IsolatedFrom(other.SessionID())` (user-role turns only;
system prompts and tool names are not compared).

See [docs/go-client.md](docs/go-client.md) for the full session, query, and
assertion API.

### Negative tests: asserting zero LLM calls

A session that never hit the model is a 404, not an empty trace. `Wait` /
`Expect` stay strict (DOG-2: a bypassed proxy must not look clean). Opt in:

```go
lm.ExpectNoCalls(ctx)                                 // 404 passes; N calls fail with the summary
lm.Expect(ctx, llmentary.AllowEmpty()).CallCount(0)   // empty Expectation
lm.Expect(ctx, llmentary.AllowEmpty()).IsolatedFrom(other.SessionID())
```

`IsolatedFrom` on a never-created other session is the assertion
`no calls recorded for session <id>` (not `SessionNotFound`); with
`AllowEmpty` it passes. Client messages contain `known sessions:` at most
once.

## Waiting for calls

The proxy `/sessions/{id}/wait` endpoint reads `timeout`, `quiet`, and
optional `min_calls` as query parameters, for example
`timeout=30s&quiet=500ms&min_calls=2`. Pass `llmentary.MinCalls(n)` to
`Wait` / `Expect` / `Check`.

| Setting | Default | Environment variable |
|---|---|---|
| Wait timeout | `30s` | `LLMENTARY_WAIT_TIMEOUT` |
| Quiet period | `500ms` | `LLMENTARY_WAIT_QUIET` |

Each variable accepts a Go duration (`45s`, `200ms`) or bare seconds (`45`,
`0.2`). A wait timeout is a clear error: `wait timed out after 30s on session
X (k calls so far, still active: n). Raise LLMENTARY_WAIT_TIMEOUT` if the
graph needs more time. The client HTTP timeout is `timeout + quiet + 5s` so
the request is not aborted before the proxy answers.

`wait` on a never-created session: `404 session_not_found` immediately when
`min_calls=0` (`IsSessionNotFound`); `504 wait_timeout` after quiet when
`min_calls>=1` (`IsWaitTimeout`).

```go
_, err := client.Wait(ctx, "ghost", 200*time.Millisecond, 3*time.Second)
if llmentary.IsSessionNotFound(err) {
    // min_calls=0: the session was never created
}
_, err = client.Wait(ctx, "ghost", 200*time.Millisecond, 3*time.Second, llmentary.MinCalls(1))
if llmentary.IsWaitTimeout(err) {
    // min_calls>=1: waited for a call that never arrived
}
```

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
