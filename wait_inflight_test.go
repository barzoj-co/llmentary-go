// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWaitTimeoutParsesPinFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "control", "wait_timeout_inflight.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		AwaitingHeaders json.RawMessage `json:"awaiting_headers"`
		Streaming       json.RawMessage `json:"streaming"`
	}
	if err := json.Unmarshal(raw, &pin); err != nil {
		t.Fatal(err)
	}

	t.Run("awaiting_headers", func(t *testing.T) {
		err := waitOn504(t, pin.AwaitingHeaders)
		wt := requireWaitTimeout(t, err)
		if !wt.ActiveKnown || wt.Active != 1 || len(wt.InFlight) != 1 {
			t.Fatalf("active=%d known=%v inflight=%d", wt.Active, wt.ActiveKnown, len(wt.InFlight))
		}
		got := wt.InFlight[0]
		if got.CallID != "call_1" || got.Seq != 1 || got.Phase != InFlightAwaitingHeaders || got.HTTPStatus != 0 {
			t.Fatalf("in-flight=%+v", got)
		}
		if got.APIFamily != "openai.responses" || got.BytesReceived != 0 || got.ElapsedMS != 1000 {
			t.Fatalf("in-flight=%+v", got)
		}
		msg := err.Error()
		for _, want := range []string{"still active: 1", "call_1", "seq=1", "openai.responses", InFlightAwaitingHeaders, "1000ms"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("missing %q in %q", want, msg)
			}
		}
		if strings.Contains(msg, "status=") {
			t.Fatalf("awaiting_headers must omit http_status: %q", msg)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		err := waitOn504(t, pin.Streaming)
		wt := requireWaitTimeout(t, err)
		if !wt.ActiveKnown || wt.Active != 1 || len(wt.InFlight) != 1 {
			t.Fatalf("active=%d known=%v inflight=%d", wt.Active, wt.ActiveKnown, len(wt.InFlight))
		}
		got := wt.InFlight[0]
		if got.Phase != InFlightStreaming || got.HTTPStatus != 200 || got.BytesReceived != 42 {
			t.Fatalf("streaming in-flight=%+v", got)
		}
		if got.APIFamily != "openai.chat" || got.Path != "/v1/chat/completions" {
			t.Fatalf("streaming in-flight=%+v", got)
		}
		msg := err.Error()
		for _, want := range []string{"still active: 1", "call_1", InFlightStreaming, "status=200"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("missing %q in %q", want, msg)
			}
		}
	})
}

func TestWaitTimeoutOldProxyFallsBackToSessionRow(t *testing.T) {
	listed := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/sess/wait":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"id":"sess","calls":[],"error":{"type":"wait_timeout","calls_seen":0,"message":"wait timed out"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions":
			listed++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessions": []SessionInfo{{ID: "sess", Calls: 0, Active: 2}},
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
	_, err = c.Wait(context.Background(), "sess", time.Millisecond, 50*time.Millisecond)
	wt := requireWaitTimeout(t, err)
	if listed == 0 {
		t.Fatal("old proxy shape must fall back to GET /sessions")
	}
	if !wt.ActiveKnown || wt.Active != 2 {
		t.Fatalf("fallback active=%d known=%v", wt.Active, wt.ActiveKnown)
	}
	if len(wt.InFlight) != 0 {
		t.Fatalf("old proxy must not invent in-flight list: %+v", wt.InFlight)
	}
	msg := err.Error()
	if !strings.Contains(msg, "still active: 2") {
		t.Fatalf("fallback message=%q", msg)
	}
	if strings.Contains(msg, "[") {
		t.Fatalf("old proxy must not list calls: %q", msg)
	}
}

func TestWaitTimeoutEmptyInFlightIsKnownZero(t *testing.T) {
	listed := 0
	body := `{"id":"sess","calls":[{"id":"c1","seq":1}],"error":{"type":"wait_timeout","calls_seen":1,"active":0,"in_flight":[]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/wait"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(body))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions":
			listed++
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Wait(context.Background(), "sess", time.Millisecond, 50*time.Millisecond)
	wt := requireWaitTimeout(t, err)
	if listed != 0 {
		t.Fatal("504 active/in_flight must skip GET /sessions")
	}
	if !wt.ActiveKnown || wt.Active != 0 {
		t.Fatalf("empty in_flight active=%d known=%v", wt.Active, wt.ActiveKnown)
	}
	if wt.InFlight == nil {
		t.Fatal("empty in_flight should be a non-nil empty slice")
	}
	if len(wt.InFlight) != 0 {
		t.Fatalf("in_flight=%+v", wt.InFlight)
	}
	msg := err.Error()
	if !strings.Contains(msg, "still active: 0") {
		t.Fatalf("known zero must be printed: %q", msg)
	}
	if strings.Contains(msg, "[") {
		t.Fatalf("empty list must not add brackets: %q", msg)
	}
}

func TestWaitTimeoutManyInFlightAreListed(t *testing.T) {
	inflight := make([]map[string]any, 0, 5)
	for i := 1; i <= 5; i++ {
		inflight = append(inflight, map[string]any{
			"call_id":        "call_" + strconv.Itoa(i),
			"seq":            i,
			"method":         "POST",
			"path":           "/v1/chat/completions",
			"api_family":     "openai.chat",
			"started_at":     "2026-10-22T07:00:00Z",
			"elapsed_ms":     i * 100,
			"phase":          InFlightAwaitingHeaders,
			"bytes_received": 0,
		})
	}
	payload, err := json.Marshal(map[string]any{
		"id":    "busy",
		"calls": []any{},
		"error": map[string]any{
			"type":       "wait_timeout",
			"message":    "wait timed out",
			"calls_seen": 0,
			"min_calls":  3,
			"active":     5,
			"in_flight":  inflight,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = waitOn504(t, payload)
	wt := requireWaitTimeout(t, err)
	if !wt.ActiveKnown || wt.Active != 5 || len(wt.InFlight) != 5 {
		t.Fatalf("active=%d known=%v n=%d", wt.Active, wt.ActiveKnown, len(wt.InFlight))
	}
	msg := err.Error()
	if !strings.Contains(msg, "still active: 5") || !strings.Contains(msg, "min_calls=3") {
		t.Fatalf("message=%q", msg)
	}
	for i := 1; i <= 5; i++ {
		want := "call_" + strconv.Itoa(i)
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %s in %q", want, msg)
		}
	}
}

func TestWaitTimeoutInFlightWithoutActiveUsesLen(t *testing.T) {
	body := `{
		"id":"sess","calls":[],
		"error":{"type":"wait_timeout","in_flight":[
			{"call_id":"call_9","seq":9,"method":"POST","path":"/v1/responses","phase":"awaiting_headers","elapsed_ms":12,"bytes_received":0,"started_at":"2026-10-22T07:00:00Z"}
		]}
	}`
	err := waitOn504(t, []byte(body))
	wt := requireWaitTimeout(t, err)
	if !wt.ActiveKnown || wt.Active != 1 || len(wt.InFlight) != 1 || wt.InFlight[0].CallID != "call_9" {
		t.Fatalf("wt=%+v inflight=%+v", wt, wt.InFlight)
	}
}

func TestWaitTimeoutNullInFlightWithActive(t *testing.T) {
	body := `{"id":"sess","calls":[],"error":{"type":"wait_timeout","active":0,"in_flight":null}}`
	err := waitOn504(t, []byte(body))
	wt := requireWaitTimeout(t, err)
	if !wt.ActiveKnown || wt.Active != 0 {
		t.Fatalf("null in_flight with active=0: known=%v active=%d", wt.ActiveKnown, wt.Active)
	}
	if len(wt.InFlight) != 0 {
		t.Fatalf("null in_flight=%+v", wt.InFlight)
	}
}

func TestWaitTimeoutInFlightBriefFallsBackToPath(t *testing.T) {
	got := InFlightCall{Path: "/v1/embeddings", Phase: InFlightStreaming, HTTPStatus: 200}.brief()
	if !strings.Contains(got, "call") || !strings.Contains(got, "/v1/embeddings") || !strings.Contains(got, "status=200") {
		t.Fatalf("brief=%q", got)
	}
	if formatInFlightList(nil) != "" || formatInFlightList([]InFlightCall{}) != "" {
		t.Fatal("empty list must format as empty")
	}
}

func waitOn504(t *testing.T, body []byte) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/wait") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write(body)
			return
		}
		if r.URL.Path == "/sessions" {
			t.Errorf("504 with in-flight fields must not list sessions")
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	id := "sess"
	var envelope struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.ID != "" {
		id = envelope.ID
	}
	_, err = c.Wait(context.Background(), id, time.Millisecond, 50*time.Millisecond)
	return err
}

func requireWaitTimeout(t *testing.T, err error) *WaitTimeoutError {
	t.Helper()
	var wt *WaitTimeoutError
	if !errors.As(err, &wt) || wt == nil {
		t.Fatalf("err=%v", err)
	}
	return wt
}
