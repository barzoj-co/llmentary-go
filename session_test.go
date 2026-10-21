// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFailNoCallsNamesSessionAndKnownIDs(t *testing.T) {
	tb := &fakeTB{}
	a := &Assertions{t: tb, trace: Trace{SessionID: "sess-zero"}}
	a.failNoCalls([]string{"alpha", "beta"})
	tb.flush()
	joined := strings.Join(tb.messages, "\n")
	for _, want := range []string{
		"no calls recorded for session sess-zero",
		"alpha",
		"beta",
		"Trace summary:",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestSessionHelpers(t *testing.T) {
	client := &http.Client{Timeout: time.Second}
	lm := New(t,
		WithProxyURL("http://127.0.0.1:7777"),
		WithControlURL("http://127.0.0.1:7778"),
		WithSessionControlToken("token"),
		WithSessionHTTPClient(client),
		WithWait(time.Millisecond, time.Second),
	)
	if lm.SessionID() == "" {
		t.Fatal("empty session id")
	}
	if got := lm.OpenAIBaseURL("openai"); got != "http://127.0.0.1:7777/u/openai" {
		t.Fatalf("OpenAIBaseURL=%q", got)
	}
	if got := lm.OpenAIBaseURL(""); got != "http://127.0.0.1:7777/v1" {
		t.Fatalf("OpenAIBaseURL(\"\")=%q", got)
	}
	if got := lm.PathBaseURL(""); got != "http://127.0.0.1:7777/s/"+lm.SessionID()+"/v1" {
		t.Fatalf("PathBaseURL(\"\")=%q", got)
	}
	if got := lm.BaseURL(""); got != "http://127.0.0.1:7777" {
		t.Fatalf("BaseURL=%q", got)
	}
	key, value := lm.Header()
	if key != SessionHeaderName || value != lm.SessionID() {
		t.Fatalf("Header()=%s %s", key, value)
	}
	if got := lm.Headers().Get(SessionHeaderName); got != lm.SessionID() {
		t.Fatalf("Headers()=%q", got)
	}
	if !strings.Contains(lm.Baggage(), lm.SessionID()) {
		t.Fatalf("Baggage()=%q", lm.Baggage())
	}
	if lm.HTTPClient() == nil {
		t.Fatal("HTTPClient is nil")
	}
}

func TestAssertionsToolAndMetadata(t *testing.T) {
	trace := fixtureTrace()
	a := &Assertions{t: t, trace: trace, fatal: true}
	a.CallCount(2)
	a.ToolCalled("get_weather", ArgsSubset(map[string]any{"city": "Paris"}), ArgPath("unit", "celsius"))
	a.ToolCalledTimes("get_weather", 1)
	a.ToolNotCalled("delete_user")
	a.ToolOrder("get_weather", "summarize")
	a.ToolBefore("get_weather", "summarize")
	a.ParallelToolCalls("get_weather", "summarize")
	a.ToolResultFedBack("call_weather")
	a.Model("gpt-test")
	a.RequestParam("temperature", 0)
	a.FinishReason("tool_calls")
	a.TokensBelow(100)
	a.LatencyBelow(2 * time.Second)
	a.TTFTBelow(time.Second)
	a.NoErrors()
	a.NoRetries()
	a.SystemPromptContains("weather bot")
	a.ResponseContains("sunny")
	a.ResponseMatches(`sun+`)
}

func TestArgsMatchCustom(t *testing.T) {
	trace := fixtureTrace()
	a := &Assertions{t: t, trace: trace, fatal: true}
	a.ToolCalled("get_weather", ArgsMatch(func(raw json.RawMessage) error {
		var args map[string]string
		if err := json.Unmarshal(raw, &args); err != nil {
			return err
		}
		if args["city"] != "Paris" {
			return errors.New("wrong city")
		}
		return nil
	}))
}

func TestQueryFilters(t *testing.T) {
	q := fixtureTrace().Query().Upstream("openai").APIType("chat").Model("gpt-test").Status(200)
	if q.Len() != 2 {
		t.Fatalf("filtered calls = %d, want 2", q.Len())
	}
	if q.Index(1).Calls()[0].Seq != 2 {
		t.Fatalf("index did not preserve call order")
	}
}

func TestResponseJSONSchemaValid(t *testing.T) {
	trace := Trace{SessionID: "s", Calls: []Call{{
		Seq:        1,
		Normalized: Normalized{OutputText: `{"answer":"ok"}`},
	}}}
	a := &Assertions{t: t, trace: trace, fatal: true}
	a.ResponseJSONValid()
	a.ResponseJSONSchemaValid(map[string]any{
		"type":     "object",
		"required": []string{"answer"},
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
	})
}

func TestTTFTBelowRequiresFirstByte(t *testing.T) {
	tb := &fakeTB{}
	a := &Assertions{t: tb, trace: Trace{SessionID: "s", Calls: []Call{{Seq: 1}}}}
	if a.TTFTBelow(time.Second) {
		t.Fatal("expected failure when no first-byte time is recorded")
	}
	if !tb.failed {
		t.Fatal("expected non-fatal failure")
	}
	joined := strings.Join(tb.messages, "\n")
	if !strings.Contains(joined, "no first-byte time was reported") {
		t.Fatalf("message=%q", joined)
	}
}

func TestJSONPathMissingKeyDoesNotMatchNull(t *testing.T) {
	var value any
	if err := json.Unmarshal([]byte(`{"present":null}`), &value); err != nil {
		t.Fatal(err)
	}
	if got, ok := jsonPath(value, "missing"); ok || got != nil {
		t.Fatalf("missing path should not match nil")
	}
}

func TestSchemaIntegerType(t *testing.T) {
	if err := validateBasicSchema(map[string]any{"n": "not-int"}, map[string]any{
		"type":     "object",
		"required": []string{"n"},
		"properties": map[string]any{
			"n": map[string]any{"type": "integer"},
		},
	}); err == nil {
		t.Fatalf("string should not satisfy integer schema")
	}
	if err := validateBasicSchema(map[string]any{"n": float64(3)}, map[string]any{
		"type":     "object",
		"required": []string{"n"},
		"properties": map[string]any{
			"n": map[string]any{"type": "integer"},
		},
	}); err != nil {
		t.Fatalf("integer-valued number should satisfy schema: %v", err)
	}
}

func TestAssertionFailureMessagesIncludeTraceSummary(t *testing.T) {
	trace := fixtureTrace()
	trace.Calls = append(trace.Calls, Call{
		ID:         "call_error",
		SessionID:  trace.SessionID,
		Seq:        3,
		Provider:   "openai",
		APIFamily:  "openai.chat",
		HTTPStatus: 200,
		Normalized: Normalized{
			FinishReason: "error",
			Error: &CallError{
				Type:      "rate_limit_error",
				Code:      "rate_limit_exceeded",
				Message:   "slow down",
				MidStream: true,
			},
		},
	})

	nonFatal := &fakeTB{}
	check := &Assertions{t: nonFatal, trace: trace, sessionTrace: trace}
	check.ToolCalled("missing_tool")
	check.FinishReason("length")
	check.CallCount(1)
	nonFatal.flush()

	joined := strings.Join(nonFatal.messages, "\n")
	for _, want := range []string{
		`expected tool "missing_tool" to be called`,
		`expected finish reason "length"`,
		`LLM call count mismatch`,
		`Trace summary:`,
		trace.SessionID,
		`get_weather args={"city":"Paris","unit":"celsius"}`,
		`finish=tool_calls`,
		`error type=rate_limit_error code=rate_limit_exceeded message=slow down mid_stream=true`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failure output missing %q:\n%s", want, joined)
		}
	}
	if strings.Count(joined, "Trace summary:") != 1 {
		t.Fatalf("soft failures should include the summary once, got %d:\n%s", strings.Count(joined, "Trace summary:"), joined)
	}
	if !nonFatal.failed || nonFatal.fatal {
		t.Fatalf("non-fatal assertions should record errors without fatal: %+v", nonFatal)
	}

	fatal := &fakeTB{}
	require := &Assertions{t: fatal, trace: trace, fatal: true}
	require.ToolCalled("missing_tool")
	if !fatal.fatal || !fatal.failed {
		t.Fatalf("fatal assertion did not record fatal failure: %+v", fatal)
	}
	if got := strings.Join(fatal.messages, "\n"); !strings.Contains(got, `expected tool "missing_tool" to be called`) || !strings.Contains(got, "Trace summary:") {
		t.Fatalf("fatal output missing assertion/summary:\n%s", got)
	}
}

type fakeTB struct {
	testing.TB
	messages []string
	failed   bool
	fatal    bool
	cleanups []func()
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Cleanup(fn func()) {
	f.cleanups = append(f.cleanups, fn)
}

func (f *fakeTB) flush() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	f.cleanups = nil
}

func (f *fakeTB) Error(args ...any) {
	f.failed = true
	f.messages = append(f.messages, fmt.Sprint(args...))
}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.failed = true
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Fatal(args ...any) {
	f.failed = true
	f.fatal = true
	f.messages = append(f.messages, fmt.Sprint(args...))
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.fatal = true
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}

func fixtureTrace() Trace {
	firstByte := time.Now().Add(50 * time.Millisecond)
	retry := 0
	return Trace{SessionID: "test-session", Calls: []Call{
		{
			ID:          "call_1",
			SessionID:   "test-session",
			Seq:         1,
			StartedAt:   time.Now(),
			FirstByteAt: &firstByte,
			LatencyMS:   100,
			Provider:    "openai",
			APIFamily:   "openai.chat",
			HTTPStatus:  200,
			RetryCount:  &retry,
			RawRequest:  []byte(`{"model":"gpt-test","temperature":0}`),
			Normalized: Normalized{
				Model:        Model{Requested: "gpt-test", Returned: "gpt-test"},
				SystemPrompt: "weather bot",
				FinishReason: "tool_calls",
				OutputText:   "sunny",
				Usage:        &Usage{TotalTokens: intPtrForTest(42)},
				ToolCalls: []ToolCall{
					{Index: 0, ID: "call_weather", Name: "get_weather", ArgumentsRaw: `{"city":"Paris","unit":"celsius"}`, Arguments: json.RawMessage(`{"city":"Paris","unit":"celsius"}`)},
					{Index: 1, ID: "call_summary", Name: "summarize", ArgumentsRaw: `{}`, Arguments: json.RawMessage(`{}`)},
				},
			},
		},
		{
			ID:         "call_2",
			SessionID:  "test-session",
			Seq:        2,
			Provider:   "openai",
			APIFamily:  "openai.chat",
			HTTPStatus: 200,
			Normalized: Normalized{
				Model:         Model{Requested: "gpt-test"},
				FinishReason:  "stop",
				ToolResultsIn: []ToolResult{{CallID: "call_weather"}},
			},
		},
	}}
}

func intPtrForTest(v int) *int { return &v }

func TestTraceSummaryOmitsUnknownFinishAndCallKeys(t *testing.T) {
	in, out := 12, 3
	trace := Trace{SessionID: "graph", Calls: []Call{
		{
			Seq:           1,
			Provider:      "openai",
			APIFamily:     "openai.chat",
			HTTPStatus:    200,
			SessionSource: "path",
			Normalized: Normalized{
				FinishReason: "stop",
				OutputText:   `{"route":"specialist"}`,
				Usage:        &Usage{InputTokens: &in, OutputTokens: &out},
			},
		},
		{
			Seq:           2,
			Provider:      "openai",
			APIFamily:     "openai.chat",
			HTTPStatus:    200,
			SessionSource: "path",
			Normalized: Normalized{
				FinishReason:  "tool_calls",
				ToolsDeclared: []ToolDecl{{Name: "get_risks"}},
				ToolCalls:     []ToolCall{{Index: 0, Name: "get_risks", ArgumentsRaw: `{"n":1}`}},
			},
		},
		{
			Seq:           3,
			Provider:      "openai",
			APIFamily:     "openai.chat",
			HTTPStatus:    200,
			SessionSource: "header",
			Normalized:    Normalized{FinishReason: "stop", OutputText: "done"},
		},
		{
			Seq:           4,
			Provider:      "openai",
			APIFamily:     "openai.chat",
			HTTPStatus:    401,
			SessionSource: "path",
			Normalized: Normalized{
				FinishReason: "unknown",
				Error:        &CallError{HTTPStatus: 401, Code: "invalid_api_key", Message: "bad key"},
			},
		},
	}}
	summary := trace.Summary()
	if strings.Contains(summary, "call keys=") {
		t.Fatalf("summary should not dump call keys:\n%s", summary)
	}
	if strings.Contains(summary, "finish=unknown") {
		t.Fatalf("summary should omit finish=unknown:\n%s", summary)
	}
	for _, line := range strings.Split(summary, "\n") {
		if strings.Contains(line, "status=401") && strings.Contains(line, "finish=") {
			t.Fatalf("401 line should omit finish=:\n%s", line)
		}
	}
	for _, want := range []string{"src=path", "src=header", "tokens=12/3", "finish=tool_calls", "finish=stop"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestCallAccessorsSessionSourceAndUpstreamURL(t *testing.T) {
	cases := []struct {
		name   string
		call   Call
		source string
		url    string
	}{
		{
			name:   "header",
			call:   Call{SessionSource: "header", Upstream: Upstream{URL: "https://api.openai.com/v1/chat/completions"}},
			source: "header",
			url:    "https://api.openai.com/v1/chat/completions",
		},
		{
			name:   "baggage",
			call:   Call{SessionSource: "baggage", Upstream: Upstream{URL: "https://api.openai.com/v1/responses"}},
			source: "baggage",
			url:    "https://api.openai.com/v1/responses",
		},
		{
			name:   "path",
			call:   Call{SessionSource: "path", Upstream: Upstream{URL: "https://api.openai.com/v1/chat/completions"}},
			source: "path",
			url:    "https://api.openai.com/v1/chat/completions",
		},
		{
			name:   "default",
			call:   Call{SessionSource: "default"},
			source: "default",
			url:    "",
		},
		{
			name:   "absent",
			call:   Call{},
			source: "",
			url:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.call.SessionSource != tc.source {
				t.Fatalf("SessionSource=%q want %q", tc.call.SessionSource, tc.source)
			}
			if got := tc.call.UpstreamURL(); got != tc.url {
				t.Fatalf("UpstreamURL()=%q want %q", got, tc.url)
			}
			if strings.Contains(tc.call.UpstreamURL(), "/s/") {
				t.Fatalf("upstream URL leaked session prefix: %q", tc.call.UpstreamURL())
			}
		})
	}
}
