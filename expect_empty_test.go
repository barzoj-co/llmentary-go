// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExpectNoCalls404AndFailureSummary(t *testing.T) {
	srv := newZeroCallServer(t, map[string][]Call{
		"used": {{ID: "call_1", Seq: 1, HTTPStatus: 200, Normalized: Normalized{FinishReason: "stop", Model: Model{Requested: "gpt-4.1-mini"}}}},
	})
	empty := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("empty"), WithWait(time.Millisecond, time.Second))
	if !empty.ExpectNoCalls(context.Background()) {
		t.Fatal("ExpectNoCalls should pass on 404")
	}

	used := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("used"), WithWait(time.Millisecond, time.Second))
	rec := &fakeTB{}
	used.t = rec
	if used.CheckNoCalls(context.Background()) {
		t.Fatal("CheckNoCalls should fail when a call exists")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	for _, want := range []string{"expected 0 LLM calls, got 1", "Trace summary:", "#1 (call_1)"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestExpectAllowEmptyCallCountZero(t *testing.T) {
	srv := newZeroCallServer(t, nil)
	lm := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("empty"), WithWait(time.Millisecond, time.Second))
	if !lm.Expect(context.Background(), AllowEmpty()).CallCount(0) {
		t.Fatal("Expect(AllowEmpty()).CallCount(0) should pass on a never-created session")
	}

	rec := &fakeTB{}
	strict := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("empty"), WithWait(time.Millisecond, time.Second))
	strict.t = rec
	_ = strict.Check(context.Background())
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	if !strings.Contains(joined, "no calls recorded for session empty") {
		t.Fatalf("strict Expect should fail DOG-2 style:\n%s", joined)
	}
}

func TestIsolatedFromUnknownOtherIDClientLookup(t *testing.T) {
	srv := newZeroCallServer(t, map[string][]Call{
		"sess-x": {{
			ID:         "call_x1",
			SessionID:  "sess-x",
			Seq:        1,
			RawRequest: []byte(`{"messages":[{"role":"user","content":"What is my biological age?"}]}`),
		}},
	})
	lm := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("sess-x"), WithWait(time.Millisecond, time.Second))
	rec := &fakeTB{}
	lm.t = rec
	if lm.Check(context.Background()).IsolatedFrom("never-created-session") {
		t.Fatal("IsolatedFrom(never-created-session) should fail without WithOtherTrace")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	if !strings.Contains(joined, "no calls recorded for session never-created-session") ||
		!strings.Contains(joined, "isolated_from needs calls on both sides") {
		t.Fatalf("client lookup unknown other:\n%s", joined)
	}
	if strings.Contains(joined, "needs WithOtherTrace") {
		t.Fatalf("should use client lookup, not require WithOtherTrace:\n%s", joined)
	}
}

func TestIsolatedFromNeverCreatedSession(t *testing.T) {
	srv := newZeroCallServer(t, map[string][]Call{
		"a": {{
			ID:         "call_a",
			SessionID:  "a",
			Seq:        1,
			RawRequest: []byte(`{"messages":[{"role":"user","content":"What is the capital of France?"}]}`),
		}},
	})
	a := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("a"), WithWait(time.Millisecond, time.Second))
	rec := &fakeTB{}
	a.t = rec
	if a.Check(context.Background()).IsolatedFrom("ghost") {
		t.Fatal("IsolatedFrom(never-created) should fail")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	if !strings.Contains(joined, "no calls recorded for session ghost") ||
		!strings.Contains(joined, "isolated_from needs calls on both sides") {
		t.Fatalf("want assertion, not raw SessionNotFound:\n%s", joined)
	}
	if strings.Contains(joined, "llmentary: wait") && strings.Contains(joined, "SessionNotFound") {
		t.Fatalf("should not leak SessionNotFound:\n%s", joined)
	}

	ok := &fakeTB{}
	a.t = ok
	if !a.Check(context.Background(), AllowEmpty()).IsolatedFrom("ghost") {
		t.Fatalf("AllowEmpty IsolatedFrom(never-created) should pass: %v", ok.messages)
	}

	typo := &fakeTB{}
	a.t = typo
	if a.Check(context.Background()).IsolatedFrom("a-typo") {
		t.Fatal("IsolatedFrom(typo id) should fail")
	}
	typo.flush()
	typoMsg := strings.Join(typo.messages, "\n")
	if !strings.Contains(typoMsg, "no calls recorded for session a-typo") ||
		!strings.Contains(typoMsg, "isolated_from needs calls on both sides") {
		t.Fatalf("typo IsolatedFrom:\n%s", typoMsg)
	}
}

func TestSessionNotFoundKnownSessionsOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sessions/missing":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"session_not_found","message":"no calls recorded for session \"missing\"; known sessions: GET /sessions"}}`))
		case "/sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessions": []SessionInfo{{ID: "known-a"}, {ID: "known-b"}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Calls(context.Background(), "missing")
	if !IsSessionNotFound(err) {
		t.Fatalf("err=%v", err)
	}
	msg := err.Error()
	if n := countKnownSessionsHints(msg); n != 1 {
		t.Fatalf("known sessions: count=%d in %q", n, msg)
	}
	if strings.Contains(msg, "GET /sessions") {
		t.Fatalf("client should not repeat the proxy hint: %q", msg)
	}
	for _, want := range []string{"known-a", "known-b", "no calls recorded for session missing"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}

	empty := newSessionNotFound("none", nil, nil).Error()
	if strings.Contains(empty, "known sessions:") {
		t.Fatalf("no known ids should omit the hint: %q", empty)
	}
}

func newZeroCallServer(t *testing.T, calls map[string][]Call) *httptest.Server {
	t.Helper()
	if calls == nil {
		calls = map[string][]Call{}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/sessions" {
			var list []SessionInfo
			for id, items := range calls {
				list = append(list, SessionInfo{ID: id, Calls: len(items)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": list})
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/sessions/"), "/wait")
		id = strings.Trim(id, "/")
		items, ok := calls[id]
		if !ok || len(items) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"session_not_found","message":"no calls recorded for session \"` + id + `\"; known sessions: GET /sessions"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(sessionPayload{ID: id, Calls: items})
	}))
	t.Cleanup(srv.Close)
	return srv
}
