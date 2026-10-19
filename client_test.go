// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
			_ = json.NewEncoder(w).Encode(Session{ID: "abc", Calls: []Call{{ID: "c1", Seq: 1}}})
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
