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

func TestNewClientRejectsRelativeURL(t *testing.T) {
	if _, err := NewClient("/sessions"); err == nil {
		t.Fatal("expected error")
	}
}

func TestClientWaitAndDelete(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/abc/wait":
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Errorf("missing bearer token")
			}
			_ = json.NewEncoder(w).Encode(sessionPayload{ID: "abc", Calls: []Call{{ID: "c1", Seq: 1}}})
		case r.Method == http.MethodDelete && r.URL.Path == "/sessions/abc":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, WithControlToken("secret"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls, err := c.Wait(ctx, "abc", 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].ID != "c1" {
		t.Fatalf("calls=%v", calls)
	}
	if err := c.Delete(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("delete not called")
	}
}

func TestClientHTTPErrorIncludesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "nope")
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Calls(context.Background(), "x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestListSessionsAndConfig(t *testing.T) {
	var gotSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sessions":
			gotSource = r.URL.Query().Get("source")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessions": []SessionInfo{{
					ID: "default", Calls: 2, Active: 0, Sources: []string{"default"},
					Spec: &SessionSpecCounts{Checked: 2, Clean: 1, Findings: 1, RequestOnly: 0, NotChecked: 0},
				}},
			})
		case "/config":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version":                 "0.2.0-test",
				"upstreams":               map[string]string{"openai": "https://api.openai.com/v1"},
				"spec_targets":            map[string]string{"openai": "openai"},
				"upstream_header_timeout": "0s",
				"upstream_idle_timeout":   "0s",
			})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.ListSessions(context.Background(), Source("header"))
	if err != nil {
		t.Fatal(err)
	}
	if gotSource != "header" || len(list) != 1 || list[0].ID != "default" || list[0].Calls != 2 {
		t.Fatalf("source=%q list=%+v", gotSource, list)
	}
	if list[0].Spec == nil || list[0].Spec.Checked != 2 || list[0].Spec.Clean != 1 || list[0].Spec.Findings != 1 {
		t.Fatalf("list spec=%+v", list[0].Spec)
	}
	cfg, err := c.Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "0.2.0-test" || cfg.Upstreams["openai"] != "https://api.openai.com/v1" {
		t.Fatalf("config=%+v", cfg)
	}
	if cfg.SpecTarget("openai") != SpecTargetOpenAI {
		t.Fatalf("spec_targets=%v", cfg.SpecTargets)
	}
	if header, ok, err := cfg.HeaderTimeout(); err != nil || !ok || header != 0 {
		t.Fatalf("header timeout=%v ok=%v err=%v", header, ok, err)
	}
	version, err := c.Version(context.Background())
	if err != nil || version != "0.2.0-test" {
		t.Fatalf("version=%q err=%v", version, err)
	}
}

func TestCallsSessionNotFound(t *testing.T) {
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
	for _, want := range []string{"no calls recorded for session missing", "known-a", "known-b"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
	if n := countKnownSessionsHints(err.Error()); n != 1 {
		t.Fatalf("known sessions: count=%d in %q", n, err)
	}
}

func TestWaitSendsMinCalls(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/wait") {
			gotQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(sessionPayload{ID: "s", Calls: []Call{{ID: "c1"}, {ID: "c2"}}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	calls, err := c.Wait(context.Background(), "s", 100*time.Millisecond, time.Second, MinCalls(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls=%d", len(calls))
	}
	if gotQuery != "timeout=1s&quiet=100ms&min_calls=2" {
		t.Fatalf("query=%q", gotQuery)
	}
}
