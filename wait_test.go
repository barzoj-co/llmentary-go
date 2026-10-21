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

func TestWaitQueryStringDefaultAndConfigured(t *testing.T) {
	t.Setenv(EnvWaitTimeout, "")
	t.Setenv(EnvWaitQuiet, "")
	t.Setenv(envWaitTimeoutMSDeprecated, "")
	t.Setenv(envWaitIdleMSDeprecated, "")

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/wait") {
			gotQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(sessionPayload{ID: "s", Calls: []Call{{ID: "c1"}}})
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	t.Run("default", func(t *testing.T) {
		gotQuery = ""
		lm := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL))
		_ = lm.Wait(context.Background())
		if gotQuery != "timeout=30s&quiet=500ms" {
			t.Fatalf("default wait query = %q, want %q", gotQuery, "timeout=30s&quiet=500ms")
		}
	})

	t.Run("configured", func(t *testing.T) {
		gotQuery = ""
		lm := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithWait(200*time.Millisecond, 90*time.Second))
		_ = lm.Wait(context.Background())
		if gotQuery != "timeout=90s&quiet=200ms" {
			t.Fatalf("configured wait query = %q, want %q", gotQuery, "timeout=90s&quiet=200ms")
		}
	})

	t.Run("min_calls", func(t *testing.T) {
		gotQuery = ""
		lm := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithWait(200*time.Millisecond, 5*time.Second))
		_ = lm.Wait(context.Background(), MinCalls(2))
		if gotQuery != "timeout=5s&quiet=200ms&min_calls=2" {
			t.Fatalf("min_calls wait query = %q", gotQuery)
		}
	})

	t.Run("allow_empty_not_in_query", func(t *testing.T) {
		gotQuery = ""
		lm := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithWait(200*time.Millisecond, 5*time.Second))
		_ = lm.Wait(context.Background(), AllowEmpty(), Since(Mark{Seq: 1}))
		if gotQuery != "timeout=5s&quiet=200ms" {
			t.Fatalf("AllowEmpty/Since must not be sent to wait: %q", gotQuery)
		}
	})

	t.Run("env", func(t *testing.T) {
		gotQuery = ""
		t.Setenv(EnvWaitTimeout, "90")
		t.Setenv(EnvWaitQuiet, "0.2")
		lm := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL))
		_ = lm.Wait(context.Background())
		if gotQuery != "timeout=90s&quiet=200ms" {
			t.Fatalf("env wait query = %q, want %q", gotQuery, "timeout=90s&quiet=200ms")
		}
		if lm.timeout != 90*time.Second || lm.quiet != 200*time.Millisecond {
			t.Fatalf("env wait config timeout=%s quiet=%s", lm.timeout, lm.quiet)
		}
	})
}

func TestParseWaitDurationTable(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		errName string
		millis  bool
	}{
		{name: EnvWaitTimeout, value: "45", want: 45 * time.Second},
		{name: EnvWaitTimeout, value: "45s", want: 45 * time.Second},
		{name: EnvWaitQuiet, value: "0.2", want: 200 * time.Millisecond},
		{name: EnvWaitQuiet, value: "200ms", want: 200 * time.Millisecond},
		{name: envWaitTimeoutMSDeprecated, value: "45000", want: 45 * time.Second, millis: true},
		{name: envWaitIdleMSDeprecated, value: "200", want: 200 * time.Millisecond, millis: true},
		{name: EnvWaitTimeout, value: "not-a-duration", errName: EnvWaitTimeout},
		{name: EnvWaitQuiet, value: "??", errName: EnvWaitQuiet},
		{name: envWaitTimeoutMSDeprecated, value: "nope", errName: envWaitTimeoutMSDeprecated, millis: true},
	}
	for _, tc := range tests {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			var got time.Duration
			var err error
			if tc.millis {
				got, err = parseWaitDurationMillis(tc.name, tc.value)
			} else {
				got, err = parseWaitDuration(tc.name, tc.value)
			}
			if tc.errName != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tc.errName) {
					t.Fatalf("error %q does not name %s", err, tc.errName)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestApplyWaitEnvDeprecatedJSNames(t *testing.T) {
	t.Setenv(envWaitTimeoutMSDeprecated, "45000")
	t.Setenv(envWaitIdleMSDeprecated, "200")
	quiet, timeout := DefaultWaitQuiet, DefaultWaitTimeout
	if err := applyWaitEnv(&quiet, &timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 45*time.Second || quiet != 200*time.Millisecond {
		t.Fatalf("deprecated env timeout=%s quiet=%s", timeout, quiet)
	}
}

func TestApplyWaitEnvInvalidNamesVariable(t *testing.T) {
	t.Setenv(EnvWaitTimeout, "bogus")
	quiet, timeout := DefaultWaitQuiet, DefaultWaitTimeout
	err := applyWaitEnv(&quiet, &timeout)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), EnvWaitTimeout) {
		t.Fatalf("error %q does not name %s", err, EnvWaitTimeout)
	}
}

func TestWaitTimeoutZeroCallsIsSessionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/empty/wait":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"error":{"type":"wait_timeout","calls_seen":0,"min_calls":1}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/empty":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"session_not_found","message":"no calls recorded for session \"empty\""}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessions": []SessionInfo{{ID: "already-there"}},
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
	_, err = c.Wait(context.Background(), "empty", DefaultWaitQuiet, time.Second, MinCalls(1))
	if !IsSessionNotFound(err) {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "no calls recorded for session empty") || !strings.Contains(err.Error(), "already-there") {
		t.Fatalf("error %q should name the session and known ids", err)
	}
}

func TestWaitTimeoutErrorMessage(t *testing.T) {
	ended := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/sess/wait":
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte("gateway timeout"))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/sess":
			_ = json.NewEncoder(w).Encode(sessionPayload{
				ID: "sess",
				Calls: []Call{
					{ID: "c1", Seq: 1, EndedAt: ended},
					{ID: "c2", Seq: 2},
				},
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
	_, err = c.Wait(context.Background(), "sess", DefaultWaitQuiet, DefaultWaitTimeout)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	want := "wait timed out after 30s on session sess (2 calls so far, still active: 1). Raise LLMENTARY_WAIT_TIMEOUT"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("timeout error = %q, want substring %q", err, want)
	}
}

func TestWaitHTTPTimeoutUsesDefaultsForZero(t *testing.T) {
	want := DefaultWaitQuiet + DefaultWaitTimeout + waitHTTPPadding
	if got := waitHTTPTimeout(0, 0); got != want {
		t.Fatalf("waitHTTPTimeout(0, 0)=%s, want %s", got, want)
	}
}

func TestFormatWaitDurationKeepsSeconds(t *testing.T) {
	if got := formatWaitDuration(90 * time.Second); got != "90s" {
		t.Fatalf("90s formatted as %q", got)
	}
	if got := formatWaitDuration(200 * time.Millisecond); got != "200ms" {
		t.Fatalf("200ms formatted as %q", got)
	}
	if got := waitQuery(DefaultWaitQuiet, DefaultWaitTimeout, 0); got != "timeout=30s&quiet=500ms" {
		t.Fatalf("default query = %q", got)
	}
	if got := waitQuery(DefaultWaitQuiet, DefaultWaitTimeout, 2); got != "timeout=30s&quiet=500ms&min_calls=2" {
		t.Fatalf("min_calls query = %q", got)
	}
}

func TestSessionSinceEmptyNoErrorsFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/wait") {
			_ = json.NewEncoder(w).Encode(sessionPayload{ID: "s", Calls: []Call{{Seq: 1, Normalized: Normalized{FinishReason: "stop"}}}})
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	lm := New(t, WithControlURL(srv.URL), WithProxyURL(srv.URL), WithWait(time.Millisecond, time.Second))
	rec := &fakeTB{}
	lm.t = rec
	if lm.Check(context.Background(), Since(Mark{Seq: 1})).NoErrors() {
		t.Fatal("Expect(Since(last)).NoErrors should fail on zero calls")
	}
	rec.flush()
	joined := strings.Join(rec.messages, "\n")
	for _, want := range []string{"no calls matched query", "since=1", "session has 1 calls"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}

	ok := &fakeTB{}
	lm.t = ok
	if !lm.Check(context.Background(), Since(Mark{Seq: 1}), AllowEmpty()).NoErrors() {
		t.Fatalf("AllowEmpty should pass: %v", ok.messages)
	}
	if ok.failed {
		t.Fatalf("AllowEmpty recorded errors: %v", ok.messages)
	}
}
