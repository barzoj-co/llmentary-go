# Go testing client

The Go module `github.com/barzoj-co/llmentary-go` (package `llmentary`) provides the
test-facing API for deterministic LLM integration tests. It never uses an LLM judge.
Assertions run against calls recorded by the local proxy.

The library is MIT-licensed. It talks to the proxy over HTTP and does not import
proxy/server packages from [barzoj-co/llmentary](https://github.com/barzoj-co/llmentary).

Related references:

- [Recorded trace data model](data-model.md)
- [Control API contract](https://github.com/barzoj-co/llmentary/blob/main/api/control/openapi.yaml)
- [Proxy, providers, and OTel](https://github.com/barzoj-co/llmentary) in the main repository

## Session per test

`New(t)` returns `*Session`. Use `lm` as the local variable:

```go
func TestAgent(t *testing.T) {
    lm := llmentary.New(t,
        llmentary.WithProxyURL("http://127.0.0.1:7777"),
        llmentary.WithControlURL("http://127.0.0.1:7778"),
    )

    baseURL := lm.OpenAIBaseURL("") // http://127.0.0.1:7777/v1 (default openai upstream)
    pathURL := lm.PathBaseURL("")         // http://127.0.0.1:7777/s/<id>/v1 (no headers)
    key, value := lm.Header()             // X-Test-Session: <unique id>
    baggage := lm.Baggage()               // llmentary.session.id=<id>
    httpClient := lm.HTTPClient()         // injects X-Test-Session

    _, _, _ = baseURL, pathURL, key
    _, _, _ = value, baggage, httpClient

    assert := lm.Expect(context.Background()) // waits for quiet session and fails fatally
    assert.CallCount(1)
}
```

`New(t)` registers `t.Cleanup` to delete the session from the proxy. It reads defaults from:

- `LLMENTARY_PROXY_URL` (default `http://127.0.0.1:7777`)
- `LLMENTARY_CONTROL_URL` (default `http://127.0.0.1:7778`)
- `LLMENTARY_CONTROL_TOKEN` for authenticated control APIs
- `LLMENTARY_WAIT_TIMEOUT` (default `30s`)
- `LLMENTARY_WAIT_QUIET` (default `500ms`)

Use `lm.Check(ctx)` for non-fatal assertions (`t.Error`) and `lm.Expect(ctx)` for fatal
assertions (`t.Fatal`). Override wait timing for one session with
`WithWait(quiet, timeout)`.

## Waiting for calls

The proxy `/sessions/{id}/wait` endpoint reads `timeout`, `quiet`, and optional
`min_calls`. Example: `timeout=30s&quiet=500ms&min_calls=2`.

```go
trace := lm.Wait(ctx, llmentary.MinCalls(2))
assert := lm.Expect(ctx, llmentary.MinCalls(2))
```

`ListSessions` is `GET /sessions`. A 404 from `GET /sessions/{id}` is
`*llmentary.SessionNotFoundError` (`llmentary.IsSessionNotFound`). Expect/Check
on a session that never recorded a call fail with
`no calls recorded for session <id>` and, when the client listed other
sessions, `; known sessions: a, b` once (never `known sessions: GET /sessions`).

## Negative tests: asserting zero LLM calls

`Expect()` on a never-created session stays strict (DOG-2). Use an explicit
opt-in:

```go
lm.ExpectNoCalls(ctx)
lm.Expect(ctx, llmentary.AllowEmpty()).CallCount(0)
lm.Expect(ctx, llmentary.AllowEmpty()).ToolNotCalled("x")
lm.Expect(ctx, llmentary.AllowEmpty()).IsolatedFrom(other.SessionID())
```

`ExpectNoCalls` treats `404 session_not_found` as success and fails a
session that recorded calls with `expected 0 LLM calls, got N` plus the
trace summary. `IsolatedFrom(<never-created>)` is that same
`no calls recorded for session <id>` assertion, not `SessionNotFound`.

`wait` on a session that was never created (or was `DELETE`d) depends on
`min_calls`:

| `min_calls` | Proxy status | Client error |
|---|---|---|
| `0` (default) | `404 session_not_found` immediately | `*SessionNotFoundError` (`IsSessionNotFound`) |
| `>= 1` | `504 wait_timeout` after quiet / timeout | `*WaitTimeoutError` (`IsWaitTimeout`) |

```go
_, err := client.Wait(ctx, "ghost", 200*time.Millisecond, 3*time.Second)
if llmentary.IsSessionNotFound(err) {
    // min_calls=0: the session was never created
}

_, err = client.Wait(ctx, "ghost", 200*time.Millisecond, 3*time.Second, llmentary.MinCalls(1))
if llmentary.IsWaitTimeout(err) {
    // min_calls>=1: the proxy waited for a call that never arrived
}
```

`lm.Version(ctx)` / `client.Version(ctx)` reads `GET /config` `version`.

| Setting | Default | Environment variable |
|---|---|---|
| Wait timeout | `30s` | `LLMENTARY_WAIT_TIMEOUT` |
| Quiet period | `500ms` | `LLMENTARY_WAIT_QUIET` |

Each variable accepts a Go duration (`45s`, `200ms`) or bare seconds (`45`,
`0.2`). A wait timeout is a clear error: `wait timed out after 30s on session
X (k calls so far, still active: n). Raise LLMENTARY_WAIT_TIMEOUT` if the
graph needs more time. The client HTTP timeout is `timeout + quiet + 5s` so
the request is not aborted before the proxy answers.

`WithWait(quiet, timeout)` overrides the environment for one session.
`Start` / `StartForTest` no longer require `WithUpstream`; the proxy defaults to
`openai=https://api.openai.com/v1`. `OpenAIBaseURL("")` is `{proxy}/v1`.
Named upstreams still use `OpenAIBaseURL("openai")` → `{proxy}/u/openai`.

## Query calls

```go
trace := lm.Wait(ctx)
chat := trace.Query().
    Upstream("openai").
    APIType("chat").
    Model("gpt-4.1-mini").
    Status(200)

chatCalls := chat.Calls()
chat.Expect(t).NoErrors()
```

Other filters:

- `Errors()`
- `Retries()`
- `Index(i)`
- `OffersTool(name)` / `WithoutTools()`
- `Since(mark)` after `m := lm.Mark(ctx)`

```go
mark := lm.Mark(ctx)
// … next turn …
assert := lm.Expect(ctx, llmentary.Since(mark))
router := trace.Query().WithoutTools()
specialist := trace.Query().OffersTool("get_risks")
```

`Summary()` prints `#<seq> (<id>)` so `Mark` / `Since` can be compared to
the trace. Call ids are unique across sessions in one proxy process; `seq`
is 1-based within the session. Do not treat `call_1` as "first call of this
session".

Scoped assertions operate only on the filtered calls:

```go
mark := lm.Mark(ctx)
// … next turn …
turn := lm.Expect(ctx, llmentary.Since(mark))
turn.CallCount(1)
turn.NoErrors()

router := trace.Query().WithoutTools()
router.Expect(t).CallCount(1)
router.Expect(t).NoErrors()
```

`NoErrors` and `ToolNotCalled` fail when a filter or `Since` matches zero
calls (`no calls matched query (without_tools, since=3); session has 8 calls`).
That is the empty-trace guard: a missing router turn must not look like a
clean one. Opt out with `llmentary.AllowEmpty()` on `Expect` / `Check`, or
`Query.AllowEmpty()`, when the test really expects zero matches. `CallCount(0)`
still asserts an empty match without the opt-out.

```go
lm.Expect(ctx, llmentary.Since(mark), llmentary.AllowEmpty()).NoErrors()
trace.Query().OffersTool("missing").AllowEmpty().Expect(t).ToolNotCalled("x")
```

Each `Call` exposes `SessionSource` (`header`, `baggage`, `path`, or
`default`) and `UpstreamURL()` (the recorded upstream URL, never the
`/s/{session}` listen prefix).

```go
src := call.SessionSource
upstream := call.UpstreamURL()
texts := call.UserTexts() // user-role turns only
```

A path prefix isolates the SDK client that uses it, not every conversation
in one process. One env-configured client is one session. Prove two
sessions stayed apart with `IsolatedFrom`. The rule is defined once in
the [isolation spec](https://github.com/barzoj-co/llmentary/blob/main/docs/isolation-spec.md)
(and linked from [black-box isolation](https://github.com/barzoj-co/llmentary/blob/main/docs/black-box.md)).

```go
lm.Expect(ctx).IsolatedFrom(other.SessionID())
lm.Expect(ctx).IsolatedFrom(other.SessionID(), llmentary.RequirePathSource())
lm.Expect(ctx).IsolatedFrom(other.SessionID(), llmentary.MinChars(8), llmentary.IgnoreTexts("ok"))
```

`IsolatedFrom` checks:

1. every call's `session_id` is this session;
2. no call id appears in both sessions;
3. no **foreign** user turn: a user-role message in this request that this
   session never introduced (the last user message of each call, excluding
   inbound history already seen) but the other session did. Comparison is
   exact after trim, whitespace collapse, and Unicode casefold — never
   substring. Equal texts both sessions sent themselves are not a leak.
   Texts shorter than `min_chars` (default 8) are ignored.

System/developer prompts and tool definitions are never compared. An empty
session fails unless `AllowEmpty`. The same canned question in two parallel
sessions therefore passes; replaying the other session's user turn as
history fails and names both `#seq (id)` values.

## Tool assertions

```go
assert.ToolCalled("get_weather")
assert.ToolNotCalled("delete_user")
assert.ToolCalledTimes("get_weather", 1)
assert.ToolCalled("get_weather",
    llmentary.ArgsSubset(map[string]any{"city": "Paris"}),
    llmentary.ArgPath("unit", "celsius"),
    llmentary.ArgsMatch(func(raw json.RawMessage) error { return nil }),
)
assert.ToolOrder("get_weather", "summarize")
assert.ToolBefore("get_weather", "summarize")
assert.ParallelToolCalls("lookup_a", "lookup_b")
assert.ToolResultFedBack("call_abc123")
```

Argument matching is JSON based:

- `ArgsSubset` recursively checks that expected keys/values are present.
- `ArgPath` supports simple dot paths and numeric array indexes, e.g. `items.0.id`.
- `ArgsMatch` accepts a custom matcher function.

## Metadata assertions

```go
assert.Model("gpt-4.1-mini")
assert.RequestParam("temperature", 0)
assert.FinishReason("tool_calls")
assert.TokensBelow(1000)
assert.TokensAtMost(1000)
assert.LatencyBelow(2 * time.Second)
assert.TTFTBelow(500 * time.Millisecond)
assert.CallCount(1, llmentary.ExcludeRetries())
assert.Retries(0)
assert.NoRetries()
assert.NoErrors()
assert.HasError()
assert.MidStreamError()
assert.SystemPromptContains("You are")
```

## Spec validation

`normalized.validation` is metadata (was the call checked, and how).
`normalized.spec_violations` is the finding list: `[]` is clean, absent means
not checked. Do not treat the validation object as a violation. The same empty
vs absent rule applies to `ignored_fields`.

`Call.SpecStatusLabel()` / `Summary()` use the same tokens as the proxy log:
`clean` (request and response checked, 0 findings), `request_only` (response
not checked — 401s and client aborts), `N violations`, `not checked`,
`skipped:<reason>`. A before-headers abort with `http_status` 0 omits
`status=` from the summary.

| Field | Meaning |
|---|---|
| `validation` | Metadata: mode, profile, spec SHA, request/response checked, skipped reason |
| `spec_violations` | Findings. `[]` = clean. Absent = not checked |
| `ignored_fields` | Vendor keys the proxy ignored (for example `enumDescriptions`) |

```go
val := call.Validation()
findings, checked := call.SpecViolations()
ignored, _ := call.IgnoredFields()
_ = trace.SpecViolations()

assert.NoSpecViolations() // documented as AssertNoSpecViolations
assert.NoIgnoredFields()  // documented as AssertNoIgnoredFields
assert.NoSpecViolations(
    llmentary.Side("request"),
    llmentary.Calls(1),
    llmentary.IgnorePaths("/foo"),
    llmentary.IgnoreKeywords("unevaluatedProperties"),
    llmentary.AllowUnvalidated(),
)
llmentary.AssertNoSpecViolations(t, trace)
```

`Summary()` prints `spec=<token>` per call, using the tokens listed above.
Failures use
`<call#> <api_family> <side> <path>: <message> — <hint>`, or
`validation was not run` when the check was off or missing.

## Response content assertions

```go
assert.PromptContains("SEED_X")
assert.ResponseContains("done")
assert.ResponseMatches(`(?i)success`)
assert.FinalResponseMentionsAny("20", "sunny")
assert.ToolResultInPrompt("get_weather")
_ = llmentary.MentionsAny(appAnswer, "20", "sunny")
assert.ResponseJSONValid()
assert.ResponseJSONSchemaValid(map[string]any{
    "type": "object",
    "required": []any{"answer"},
    "properties": map[string]any{
        "answer": map[string]any{"type": "string"},
    },
})
```

The schema validator is intentionally small and dependency-free. It supports `type`,
`required`, and nested `properties` for common structured-output tests.

## Failure output

Failures include a compact trace summary:

```text
expected tool "get_weather" to be called

Trace summary:
session 9f...: 2 call(s)
  #1 (call_1) openai openai.chat status=200 finish=tool_calls model=gpt-4.1-mini/gpt-4.1-mini latency=730ms retry=0 spec=not checked src=path tokens=120/18
    tool[0] get_weather args={"city":"Paris"}
```

`finish=` is omitted when the call has no finish reason (HTTP errors print
`finish=unknown` from some proxies; the client drops empty and `unknown`).
`src=` and `tokens=<in>/<out>` are included when present. Hard `Expect`
failures keep the summary on the assertion. Soft `Check` failures print each
assertion without the summary, then one summary when the test ends.

The package also keeps the lower-level client methods (`Calls`, `Wait`,
`ListSessions`, `Config`, `Version`, `OTLP`, `Delete`) for custom assertions.

## Binary resolution

`ResolveBinary` and `Start` / `StartForTest` locate the proxy as `llmentary` on
`PATH`, or via `LLMENTARY_BIN`. See [Proxy binary resolution](binaries.md).

## Two-step tool flows

For Chat Completions, send the assistant message with `tool_calls` back in the next
request, followed by a `role=tool` message with the `tool_call_id`:

```go
messages = append(messages, first.Choices[0].Message.ToParam())
messages = append(messages, openai.ToolMessage(`{"city":"Paris"}`, toolCall.ID))
```

For Responses, keep the first response stored and send a `function_call_output` with
`previous_response_id`:

```json
{
  "previous_response_id": "resp_...",
  "input": [
    {"type": "function_call_output", "call_id": "call_...", "output": "{\"city\":\"Paris\"}"}
  ]
}
```

## Fixture testing

See [`examples/go/weather_test.go`](../examples/go/weather_test.go) for a complete
proxy-backed example using `openai-go` with `lm.OpenAIBaseURL("openai")` and
`lm.HTTPClient()`. The example starts the proxy via `StartForTest` against a mock
upstream.

For CI wiring, see [GitHub Actions and CI](ci.md). For proxy flags and control API
details, see the [llmentary documentation](https://github.com/barzoj-co/llmentary).
Black-box setups: [docs/black-box.md](https://github.com/barzoj-co/llmentary/blob/main/docs/black-box.md).
Trace vs free-text assertions: [docs/assertions-guide.md](https://github.com/barzoj-co/llmentary/blob/main/docs/assertions-guide.md).
