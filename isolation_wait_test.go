// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIsolatedFromWaitsOtherInFlightLeak(t *testing.T) {
	secret := "Y follow-up that X must never see (nonce-dog37)"
	yOpen := isolationCall("y", "call_y1", 1, "header", `{"messages":[{"role":"user","content":"Y opening question (nonce-dog37)"}]}`)
	ySecret := isolationCall("y", "call_y2", 2, "header", `{"messages":[{"role":"user","content":"`+secret+`"}]}`)
	xCall := isolationCall("x", "call_x1", 1, "header", `{
		"messages":[
			{"role":"user","content":"`+secret+`"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"X asks its own question"}
		]
	}`)

	var mu sync.Mutex
	var yWaitHits, yGetHits int
	var yWaitQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/sessions" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessions": []SessionInfo{
					{ID: "x", Calls: 1, Active: 0},
					{ID: "y", Calls: 2, Active: 0},
				},
			})
			return
		}
		id, wait := isolationSessionPath(r.URL.Path)
		mu.Lock()
		if id == "y" {
			if wait {
				yWaitHits++
				yWaitQuery = r.URL.RawQuery
			} else {
				yGetHits++
			}
		}
		mu.Unlock()
		switch {
		case id == "x":
			writeSessionJSON(w, "x", []Call{xCall})
		case id == "y" && wait:
			writeSessionJSON(w, "y", []Call{yOpen, ySecret})
		case id == "y":
			writeSessionJSON(w, "y", []Call{yOpen})
		default:
			writeSessionNotFound(w, id)
		}
	}))
	t.Cleanup(srv.Close)

	x := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("x"), WithWait(200*time.Millisecond, 2*time.Second))
	rec := &fakeTB{}
	x.t = rec
	if x.Check(context.Background()).IsolatedFrom("y") {
		t.Fatal("IsolatedFrom should fail after waiting Y's in-flight leak")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	for _, want := range []string{secret, "also introduced by", "call_y2", "session y"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if yWaitHits == 0 {
		t.Fatal("IsolatedFrom must wait on the other session")
	}
	if yGetHits != 0 {
		t.Fatalf("IsolatedFrom must not snapshot Y with GET, getHits=%d", yGetHits)
	}
	if !strings.Contains(yWaitQuery, "timeout=2s") || !strings.Contains(yWaitQuery, "quiet=200ms") {
		t.Fatalf("other wait query=%q want session WithWait bounds", yWaitQuery)
	}
}

func TestIsolatedFromOtherOnlyInFlight(t *testing.T) {
	xCall := isolationCall("x", "call_x1", 1, "header", `{"messages":[{"role":"user","content":"X asks its own question"}]}`)
	srv := newIsolationWaitServer(t, isolationWaitStub{
		x:        []Call{xCall},
		yWaitErr: http.StatusGatewayTimeout,
		sessions: []SessionInfo{
			{ID: "x", Calls: 1, Active: 0},
			{ID: "y", Calls: 0, Active: 1},
		},
	})

	x := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("x"), WithWait(time.Millisecond, 200*time.Millisecond))
	rec := &fakeTB{}
	x.t = rec
	if x.Check(context.Background()).IsolatedFrom("y") {
		t.Fatal("only-in-flight other should fail after wait timeout")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	if !strings.Contains(joined, "wait timed out on session y") ||
		!strings.Contains(joined, "session y has 1 call still in flight") {
		t.Fatalf("want in-flight timeout, got:\n%s", joined)
	}
	if strings.Contains(joined, "no calls recorded") {
		t.Fatalf("must not look like an empty session:\n%s", joined)
	}

	ok := &fakeTB{}
	x.t = ok
	if !x.Check(context.Background(), AllowEmpty()).IsolatedFrom("y") {
		t.Fatalf("AllowEmpty should pass after timeout with 0 settled: %v", ok.messages)
	}
}

func TestIsolatedFromPrefetchedTraceNotRefetched(t *testing.T) {
	secret := "Y follow-up that X must never see (nonce-prefetch)"
	yOpen := isolationCall("y", "call_y1", 1, "header", `{"messages":[{"role":"user","content":"Y opening question (nonce-prefetch)"}]}`)
	ySecret := isolationCall("y", "call_y2", 2, "header", `{"messages":[{"role":"user","content":"`+secret+`"}]}`)
	xCall := isolationCall("x", "call_x1", 1, "header", `{
		"messages":[
			{"role":"user","content":"`+secret+`"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"X asks its own question"}
		]
	}`)
	var yWaitHits int
	srv := newIsolationWaitServer(t, isolationWaitStub{
		x: []Call{xCall},
		y: []Call{yOpen, ySecret},
		onYWait: func() {
			yWaitHits++
		},
		sessions: []SessionInfo{{ID: "x", Calls: 1}, {ID: "y", Calls: 2}},
	})

	x := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("x"), WithWait(time.Millisecond, time.Second))
	tb := &fakeTB{}
	x.t = tb
	if !x.Check(context.Background()).IsolatedFrom("y", WithOtherTrace(Trace{SessionID: "y", Calls: []Call{yOpen}})) {
		t.Fatalf("pre-fetched stale Y should be used as given: %v", tb.messages)
	}
	if yWaitHits != 0 {
		t.Fatalf("WithOtherTrace must not wait, hits=%d", yWaitHits)
	}

	rec := &fakeTB{}
	x.t = rec
	if x.Check(context.Background()).IsolatedFrom("y") {
		t.Fatal("id form should wait and see the leak")
	}
	if yWaitHits == 0 {
		t.Fatal("IsolatedFrom(id) should wait")
	}
}

func TestIsolatedFromOtherDeletedMidWait(t *testing.T) {
	xCall := isolationCall("x", "call_x1", 1, "header", `{"messages":[{"role":"user","content":"X asks its own question"}]}`)
	srv := newIsolationWaitServer(t, isolationWaitStub{
		x:        []Call{xCall},
		yWaitErr: http.StatusNotFound,
		sessions: []SessionInfo{{ID: "x", Calls: 1, Active: 0}},
	})
	x := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("x"), WithWait(time.Millisecond, time.Second))
	rec := &fakeTB{}
	x.t = rec
	if x.Check(context.Background()).IsolatedFrom("y") {
		t.Fatal("deleted other is empty (DOG-32)")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	if !strings.Contains(joined, "no calls recorded for session y") ||
		!strings.Contains(joined, "isolated_from needs calls on both sides") {
		t.Fatalf("deleted other:\n%s", joined)
	}
	ok := &fakeTB{}
	x.t = ok
	if !x.Check(context.Background(), AllowEmpty()).IsolatedFrom("y") {
		t.Fatalf("AllowEmpty deleted other: %v", ok.messages)
	}
}

func TestIsolatedFromOtherTimeoutKeepsSettledCalls(t *testing.T) {
	yOpen := isolationCall("y", "call_y1", 1, "header", `{"messages":[{"role":"user","content":"Y opening question (nonce-timeout)"}]}`)
	xCall := isolationCall("x", "call_x1", 1, "header", `{"messages":[{"role":"user","content":"X asks its own question"}]}`)
	srv := newIsolationWaitServer(t, isolationWaitStub{
		x:         []Call{xCall},
		yWait:     []Call{yOpen},
		yWaitErr:  http.StatusGatewayTimeout,
		yWaitSeen: 1,
		sessions: []SessionInfo{
			{ID: "x", Calls: 1, Active: 0},
			{ID: "y", Calls: 1, Active: 1},
		},
	})
	x := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("x"), WithWait(time.Millisecond, 200*time.Millisecond))
	rec := &fakeTB{}
	x.t = rec
	if x.Check(context.Background()).IsolatedFrom("y") {
		t.Fatal("timeout with settled Y and remaining in-flight should fail")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	if !strings.Contains(joined, "wait timed out on session y") ||
		!strings.Contains(joined, "1 call still in flight") {
		t.Fatalf("want timeout on settled+in-flight:\n%s", joined)
	}
	if strings.Contains(joined, "no calls recorded") {
		t.Fatalf("settled calls exist:\n%s", joined)
	}
}

func TestIsolatedFromOtherTimeoutActiveZeroIsIsolated(t *testing.T) {
	yOpen := isolationCall("y", "call_y1", 1, "header", `{"messages":[{"role":"user","content":"Y opening question (nonce-quiet)"}]}`)
	xCall := isolationCall("x", "call_x1", 1, "header", `{"messages":[{"role":"user","content":"X asks its own question"}]}`)
	srv := newIsolationWaitServer(t, isolationWaitStub{
		x:         []Call{xCall},
		yWait:     []Call{yOpen},
		yWaitErr:  http.StatusGatewayTimeout,
		yWaitSeen: 1,
		sessions: []SessionInfo{
			{ID: "x", Calls: 1, Active: 0},
			{ID: "y", Calls: 1, Active: 0},
		},
	})
	x := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("x"), WithWait(time.Millisecond, 200*time.Millisecond))
	tb := &fakeTB{}
	x.t = tb
	if !x.Check(context.Background()).IsolatedFrom("y") {
		t.Fatalf("timeout with Active=0 and isolated settled calls should pass: %v", tb.messages)
	}

	secret := "Y follow-up that X must never see (nonce-quiet-leak)"
	yLeak := isolationCall("y", "call_y2", 2, "header", `{"messages":[{"role":"user","content":"`+secret+`"}]}`)
	xReplay := isolationCall("x", "call_x2", 1, "header", `{
		"messages":[
			{"role":"user","content":"`+secret+`"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"X asks its own question"}
		]
	}`)
	leakSrv := newIsolationWaitServer(t, isolationWaitStub{
		x:         []Call{xReplay},
		yWait:     []Call{yOpen, yLeak},
		yWaitErr:  http.StatusGatewayTimeout,
		yWaitSeen: 2,
		sessions: []SessionInfo{
			{ID: "x", Calls: 1, Active: 0},
			{ID: "y", Calls: 2, Active: 0},
		},
	})
	x2 := New(t, WithControlURL(leakSrv.URL), WithProxyURL(leakSrv.URL), WithSessionID("x"), WithWait(time.Millisecond, 200*time.Millisecond))
	rec := &fakeTB{}
	x2.t = rec
	if x2.Check(context.Background()).IsolatedFrom("y") {
		t.Fatal("settled leak must still fail when Active=0")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	if !strings.Contains(joined, secret) || !strings.Contains(joined, "also introduced by") {
		t.Fatalf("want leak, got:\n%s", joined)
	}
	if strings.Contains(joined, "still in flight") {
		t.Fatalf("Active=0 must not claim in-flight:\n%s", joined)
	}
}

func TestIsolatedFromBothStreamingWaitsOther(t *testing.T) {
	xCall := isolationCall("x", "call_x1", 1, "header", `{"messages":[{"role":"user","content":"X streaming question about barges"}]}`)
	yCall := isolationCall("y", "call_y1", 1, "header", `{"messages":[{"role":"user","content":"Y streaming question about weather"}]}`)
	var mu sync.Mutex
	var xWait, yWait int
	srv := newIsolationWaitServer(t, isolationWaitStub{
		x: []Call{xCall},
		y: []Call{yCall},
		onXWait: func() {
			mu.Lock()
			xWait++
			mu.Unlock()
		},
		onYWait: func() {
			mu.Lock()
			yWait++
			mu.Unlock()
		},
		sessions: []SessionInfo{{ID: "x", Calls: 1}, {ID: "y", Calls: 1}},
	})
	x := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithSessionID("x"), WithWait(time.Millisecond, time.Second))
	tb := &fakeTB{}
	x.t = tb
	if !x.Check(context.Background()).IsolatedFrom("y") {
		t.Fatalf("distinct streaming sessions should pass after both waits: %v", tb.messages)
	}
	mu.Lock()
	defer mu.Unlock()
	if xWait == 0 || yWait == 0 {
		t.Fatalf("both sides must be waited, xWait=%d yWait=%d", xWait, yWait)
	}
}

type isolationWaitStub struct {
	x         []Call
	y         []Call
	yWait     []Call
	yWaitErr  int
	yWaitSeen int
	sessions  []SessionInfo
	onXWait   func()
	onYWait   func()
}

func newIsolationWaitServer(t *testing.T, stub isolationWaitStub) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/sessions" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": stub.sessions})
			return
		}
		id, wait := isolationSessionPath(r.URL.Path)
		if id == "x" && wait && stub.onXWait != nil {
			stub.onXWait()
		}
		if id == "y" && wait && stub.onYWait != nil {
			stub.onYWait()
		}
		switch id {
		case "x":
			writeSessionJSON(w, "x", stub.x)
		case "y":
			if wait && stub.yWaitErr == http.StatusGatewayTimeout {
				calls := stub.yWait
				seen := stub.yWaitSeen
				if seen == 0 {
					seen = len(calls)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusGatewayTimeout)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":    "y",
					"calls": calls,
					"error": map[string]any{"type": "wait_timeout", "calls_seen": seen, "message": "wait timed out"},
				})
				return
			}
			if wait && stub.yWaitErr == http.StatusNotFound {
				writeSessionNotFound(w, "y")
				return
			}
			writeSessionJSON(w, "y", stub.y)
		default:
			writeSessionNotFound(w, id)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func isolationSessionPath(p string) (id string, wait bool) {
	p = strings.TrimPrefix(p, "/sessions/")
	if strings.HasSuffix(p, "/wait") {
		return strings.TrimSuffix(p, "/wait"), true
	}
	return strings.Trim(p, "/"), false
}

func writeSessionJSON(w http.ResponseWriter, id string, calls []Call) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionPayload{ID: id, Calls: calls})
}

func writeSessionNotFound(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":{"type":"session_not_found","message":"no calls recorded for session \"` + id + `\""},"id":"` + id + `"}`))
}
