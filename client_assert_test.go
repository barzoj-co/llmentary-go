// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientRejectsUnparseableURL(t *testing.T) {
	if _, err := NewClient("http://[::1"); err == nil {
		t.Fatal("expected parse error")
	}
}

func toolCalls(name string, args json.RawMessage) []Call {
	return []Call{{
		ID: "c1",
		Normalized: Normalized{
			ToolCalls: []ToolCall{{Name: name, Arguments: args}},
		},
	}}
}

func TestPackageToolAssertions(t *testing.T) {
	calls := toolCalls("get_weather", json.RawMessage(`{"city":"Paris"}`))
	AssertToolCalled(t, calls, "get_weather", map[string]string{"city": "Paris"})
	ToolCalled(t, calls, "get_weather", nil)
	AssertToolNotCalled(t, calls, "delete_user")
	ToolNotCalled(t, calls, "delete_user")
	AssertToolOrder(t, calls, "get_weather")
	ToolOrder(t, calls, "get_weather")

	tb := &fakeTB{}
	AssertToolCalled(tb, calls, "get_weather", map[string]string{"city": "Lyon"})
	if !tb.fatal || !strings.Contains(strings.Join(tb.messages, "\n"), `expected tool "get_weather"`) {
		t.Fatalf("mismatch args should fail: %+v", tb)
	}

	tb = &fakeTB{}
	AssertToolCalled(tb, calls, "missing", nil)
	if !tb.fatal {
		t.Fatal("missing tool should fail")
	}

	tb = &fakeTB{}
	AssertToolNotCalled(tb, calls, "get_weather")
	if !tb.fatal || !strings.Contains(strings.Join(tb.messages, "\n"), `expected tool "get_weather" not to be called`) {
		t.Fatalf("ToolNotCalled should fail: %+v", tb)
	}

	tb = &fakeTB{}
	AssertToolOrder(tb, calls, "other")
	if !tb.fatal || !strings.Contains(strings.Join(tb.messages, "\n"), "tool order mismatch") {
		t.Fatalf("ToolOrder should fail: %+v", tb)
	}
}

func TestPackageCallCountAndTokens(t *testing.T) {
	retry := 2
	calls := []Call{
		{ID: "a", RetryCount: intPtrForTest(0), Normalized: Normalized{Usage: &Usage{TotalTokens: intPtrForTest(10)}}},
		{ID: "b", RetryCount: &retry, Normalized: Normalized{Usage: &Usage{InputTokens: intPtrForTest(3), OutputTokens: intPtrForTest(2)}}},
	}
	AssertCallCount(t, calls, 2)
	CallCount(t, calls, 1, ExcludeRetries())
	AssertTokensBelow(t, calls, 20)
	TokensBelow(t, calls, 16)

	tb := &fakeTB{}
	AssertCallCount(tb, calls, 9)
	if !tb.fatal || !strings.Contains(strings.Join(tb.messages, "\n"), "call count mismatch") {
		t.Fatalf("CallCount should fail: %+v", tb)
	}

	retry = 1
	mixed := []Call{
		{ID: "ok", Seq: 1},
		{ID: "abort", Seq: 2, ClientAborted: true, HTTPStatus: 200, AbortPhase: AbortPhaseAfterHeaders},
		{ID: "retry-abort", Seq: 3, ClientAborted: true, RetryCount: &retry},
		{ID: "retry-ok", Seq: 4, RetryCount: &retry},
	}
	AssertCallCount(t, mixed, 4, IncludeCancelled())
	CallCount(t, mixed, 2, ExcludeAborted())
	CallCount(t, mixed, 2, ExcludeRetries(), IncludeCancelled())
	CallCount(t, mixed, 1, ExcludeAborted(), ExcludeRetries())
	tb = &fakeTB{}
	AssertCallCount(tb, mixed, 3, ExcludeAborted())
	if !tb.fatal {
		t.Fatal("ExcludeAborted count 3 should fail on 2 committed")
	}

	tb = &fakeTB{}
	AssertTokensBelow(tb, calls, 10)
	if !tb.fatal || !strings.Contains(strings.Join(tb.messages, "\n"), "token total too high") {
		t.Fatalf("TokensBelow should fail: %+v", tb)
	}

	tb = &fakeTB{}
	AssertTokensBelow(tb, []Call{{ID: "empty"}}, 10)
	if !tb.fatal || !strings.Contains(strings.Join(tb.messages, "\n"), "no token usage") {
		t.Fatalf("no usage should fail: %+v", tb)
	}
}

func countOutcomeCalls() []Call {
	return []Call{
		{ID: "committed", Seq: 1, HTTPStatus: 200, Normalized: Normalized{FinishReason: "stop"}},
		{ID: "cancelled", Seq: 2, HTTPStatus: 200, ClientAborted: true, AbortPhase: AbortPhaseAfterHeaders},
		{ID: "failed", Seq: 3, HTTPStatus: 500},
	}
}

func TestCallCountExcludesCancelledByDefault(t *testing.T) {
	calls := countOutcomeCalls()
	AssertCallCount(t, calls, 2)
	CallCount(t, calls, 2)
	CallCount(t, calls, 3, IncludeCancelled())
	CallCount(t, calls, 2, ExcludeAborted())

	tb := &fakeTB{}
	AssertCallCount(tb, calls, 3)
	joined := strings.Join(tb.messages, "\n")
	if !tb.fatal {
		t.Fatalf("cancelled calls should not count by default: %+v", tb)
	}
	for _, want := range []string{"cancelled excluded: 1", "IncludeCancelled()"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failure message missing %q:\n%s", want, joined)
		}
	}

	tb = &fakeTB{}
	AssertCallCount(tb, calls, 1)
	if !tb.fatal {
		t.Fatalf("failed calls must still count: %+v", tb)
	}

	tb = &fakeTB{}
	AssertCallCount(tb, calls, 2, IncludeCancelled())
	if !tb.fatal {
		t.Fatalf("IncludeCancelled count 2 should fail on 3: %+v", tb)
	}

	trace := Trace{SessionID: "counts", Calls: calls}
	tb = &fakeTB{}
	if trace.Query().Check(tb).CallCount(3) {
		t.Fatal("Assertions.CallCount should exclude cancelled calls by default")
	}
	if !tb.failed {
		t.Fatalf("Assertions.CallCount(3) should record a failure: %+v", tb)
	}
	tb = &fakeTB{}
	if !trace.Query().Check(tb).CallCount(2) {
		t.Fatalf("Assertions.CallCount(2) should pass: %v", tb.messages)
	}
	if !trace.Query().Check(tb).CallCount(3, IncludeCancelled()) {
		t.Fatalf("Assertions.CallCount(3, IncludeCancelled) should pass: %v", tb.messages)
	}
	if !trace.Query().Check(tb).CallCount(2, ExcludeAborted()) {
		t.Fatalf("Assertions.CallCount(2, ExcludeAborted) should pass: %v", tb.messages)
	}
}

func TestCallCountExcludeAbortedStillWorks(t *testing.T) {
	calls := append(countOutcomeCalls(),
		Call{ID: "alias-abort", Seq: 4, HTTPStatus: 0, Normalized: Normalized{ClientAborted: true}},
		Call{ID: "aborted-upstream-error", Seq: 5, HTTPStatus: 502, ClientAborted: true, Normalized: Normalized{Error: &CallError{Type: "upstream_error", Source: "upstream"}}},
	)
	if got := countCalls(calls, applyCallCountOptions(nil)); got != 3 {
		t.Fatalf("default count=%d want 3 (committed, failed, aborted-with-upstream-error)", got)
	}
	AssertCallCount(t, calls, 2, ExcludeAborted())
	CallCount(t, calls, 5, IncludeCancelled())

	tb := &fakeTB{}
	AssertCallCount(tb, calls, 3, ExcludeAborted())
	if !tb.fatal {
		t.Fatalf("ExcludeAborted count 3 should fail on 2 counted calls: %+v", tb)
	}

	trace := Trace{SessionID: "aborts", Calls: calls}
	tb = &fakeTB{}
	if !trace.Query().Check(tb).CallCount(2, ExcludeAborted()) {
		t.Fatalf("Assertions.CallCount(2, ExcludeAborted) should pass: %v", tb.messages)
	}
}

func TestAssertionsCallCountFailureMentionsExcludedCancelled(t *testing.T) {
	trace := Trace{SessionID: "hint", Calls: []Call{
		{ID: "committed", Seq: 1, HTTPStatus: 200, Normalized: Normalized{FinishReason: "stop"}},
		{ID: "cancelled", Seq: 2, HTTPStatus: 200, ClientAborted: true, AbortPhase: AbortPhaseAfterHeaders},
	}}
	tb := &fakeTB{}
	if trace.Query().Check(tb).CallCount(2) {
		t.Fatal("Assertions.CallCount(2) should fail when a cancelled call is excluded")
	}
	if !tb.failed {
		t.Fatalf("Assertions.CallCount(2) should record a failure: %+v", tb)
	}
	joined := strings.Join(tb.messages, "\n")
	if !strings.Contains(joined, "LLM call count mismatch: got 1, want 2 (cancelled excluded: 1; use IncludeCancelled() to count them)") {
		t.Fatalf("failure message missing the excluded-cancellation hint:\n%s", joined)
	}
	if strings.Count(joined, "LLM call count mismatch") != 1 {
		t.Fatalf("hint should extend the mismatch message, not duplicate it:\n%s", joined)
	}
}

func TestJSONMatchesAndMustJSON(t *testing.T) {
	if !jsonMatches(t, nil, nil) {
		t.Fatal("nil want should match")
	}
	if !jsonMatches(t, json.RawMessage(``), map[string]any{}) {
		t.Fatal("empty raw should match empty object")
	}
	if jsonMatches(t, json.RawMessage(`not-json`), map[string]any{"a": 1}) {
		t.Fatal("invalid raw should not match")
	}
	if !jsonMatches(t, json.RawMessage(`{"a":1}`), map[string]int{"a": 1}) {
		t.Fatal("equivalent JSON should match")
	}
	if got := mustJSON(nil); got != "<any>" {
		t.Fatalf("mustJSON(nil)=%q", got)
	}
	if got := mustJSON(map[string]int{"n": 1}); got != `{"n":1}` {
		t.Fatalf("mustJSON=%q", got)
	}
	if got := mustJSON(make(chan int)); got == "<any>" || got == "" {
		t.Fatalf("mustJSON(chan)=%q", got)
	}
}

func TestClientSessionHeaderAndOTLP(t *testing.T) {
	c, err := NewClient("http://127.0.0.1:7778")
	if err != nil {
		t.Fatal(err)
	}
	key, value := c.SessionHeader("sess-1")
	if key != SessionHeaderName || value != "sess-1" {
		t.Fatalf("SessionHeader=%s %s", key, value)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sessions/s1/otlp" {
			_, _ = io.WriteString(w, `{"resourceSpans":[]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c, err = NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.OTLP(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "resourceSpans") {
		t.Fatalf("otlp=%s", raw)
	}

	if _, err := c.OTLP(context.Background(), "missing"); err == nil {
		t.Fatal("expected OTLP error")
	}
}

func TestListSessionsNilPayloadAndVersionEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sessions":
			_, _ = io.WriteString(w, `{"sessions":null}`)
		case "/config":
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("nil sessions should become empty slice: %#v", list)
	}
	version, err := c.Version(context.Background())
	if err != nil || version != "" {
		t.Fatalf("empty config version=%q err=%v", version, err)
	}
}

func TestClientDeleteErrorAndWaitHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, WithHTTPClient(nil))
	if err != nil {
		t.Fatal(err)
	}
	err = c.Delete(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "status=500") {
		t.Fatalf("delete err=%v", err)
	}

	short := &http.Client{Timeout: 10 * time.Millisecond}
	c, err = NewClient(srv.URL, WithHTTPClient(short))
	if err != nil {
		t.Fatal(err)
	}
	cloned := c.waitHTTPClient(time.Second)
	if cloned == short || cloned.Timeout != time.Second {
		t.Fatalf("waitHTTPClient should clone when timeout is too short: %+v", cloned)
	}
	same := c.waitHTTPClient(time.Millisecond)
	if same != short {
		t.Fatal("waitHTTPClient should reuse client when timeout is enough")
	}
}

func TestStatusErrorStringAndNilSessionNotFound(t *testing.T) {
	se := &statusError{op: "get", status: 418, body: "nope"}
	if got := se.Error(); !strings.Contains(got, "status=418") || !strings.Contains(got, "nope") {
		t.Fatalf("statusError=%q", got)
	}
	var missing *SessionNotFoundError
	if got := missing.Error(); got != "no calls recorded for session" {
		t.Fatalf("nil SessionNotFoundError=%q", got)
	}
}
