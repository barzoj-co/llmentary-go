# llmentary-go

[![CI](https://github.com/barzoj-co/llmentary-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/barzoj-co/llmentary-go/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/barzoj-co/llmentary-go/graph/badge.svg)](https://codecov.io/gh/barzoj-co/llmentary-go)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/barzoj-co/llmentary-go/badge)](https://scorecard.dev/viewer/?uri=github.com/barzoj-co/llmentary-go)
[![License: MIT](https://img.shields.io/github/license/barzoj-co/llmentary-go)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/barzoj-co/llmentary-go.svg)](https://pkg.go.dev/github.com/barzoj-co/llmentary-go)
[![Release](https://img.shields.io/github/v/release/barzoj-co/llmentary-go?sort=semver)](https://github.com/barzoj-co/llmentary-go/releases)

Go client for [llmentary](https://github.com/barzoj-co/llmentary), a recording
proxy for testing LLM applications. Your test sends OpenAI SDK requests through
the proxy to the real provider, then asserts on what was recorded: tools called
and their arguments, call count, tokens, latency, errors.

Documentation: **https://barzoj-co.github.io/llmentary/**

## Install

```bash
go get github.com/barzoj-co/llmentary-go
go install github.com/barzoj-co/llmentary/cmd/llmentary@latest   # the proxy
```

Start the proxy before running tests:

```bash
llmentary --listen 127.0.0.1:7777 --control 127.0.0.1:7778
```

Or start it from the test with `llmentary.StartForTest(t)`.

## Quickstart

```go
func TestModelCallsWeatherTool(t *testing.T) {
	ctx := context.Background()
	lm := llmentary.New(t)

	client := openai.NewClient(
		option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
		option.WithBaseURL(lm.OpenAIBaseURL("")),
		option.WithHTTPClient(lm.HTTPClient()), // tags requests with this test's session
	)
	_, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    "gpt-4.1-mini",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("What's the weather in Paris?")},
		Tools:    []openai.ChatCompletionToolParam{weatherTool},
	})
	if err != nil {
		t.Fatal(err)
	}

	check := lm.Expect(ctx)
	check.CallCount(1)
	check.ToolCalled("get_weather", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
}
```

The full runnable file, with imports and the tool definition, is in the
[Go getting-started guide](https://barzoj-co.github.io/llmentary/getting-started/go/).

## Assertions

- Tools: `ToolCalled`, `ToolNotCalled`, `ToolOrder`, `ArgsSubset`, `ToolResultInPrompt`.
- Requests: `Model`, `RequestParam`, `PromptContains`, `SystemPromptContains`.
- Reliability: `CallCount`, `NoRetries`, `NoErrors`, `Aborted`.
- Budgets: `TokensAtMost`, `LatencyBelow`, `TTFTBelow`.
- Isolation between sessions: `IsolatedFrom`
  ([rules](https://github.com/barzoj-co/llmentary/blob/main/docs/isolation-spec.md),
  [test vectors](https://github.com/barzoj-co/llmentary/tree/main/testdata/isolation)).

All assertions: [reference](https://barzoj-co.github.io/llmentary/reference/assertions/).
Go API: [pkg.go.dev](https://pkg.go.dev/github.com/barzoj-co/llmentary-go) and
[docs/go-client.md](docs/go-client.md).

## Waiting for calls

`lm.Expect(ctx)` waits until the session has had no new calls for 500 ms
(timeout 30 s) before checking. Change this with `llmentary.WithWait`, or
`LLMENTARY_WAIT_QUIET` and `LLMENTARY_WAIT_TIMEOUT`. To check one turn of a
conversation, see
[Test one turn](https://barzoj-co.github.io/llmentary/how-to/scope-a-turn/).

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
