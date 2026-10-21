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
