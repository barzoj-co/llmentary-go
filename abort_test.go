// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCallAbortFieldsFromNewAndOldJSON(t *testing.T) {
	t.Run("top_level", func(t *testing.T) {
		var call Call
		if err := json.Unmarshal([]byte(`{
			"id":"call_1",
			"seq":1,
			"http_status":0,
			"client_aborted":true,
			"abort_phase":"before_headers",
			"normalized":{"finish_reason":"cancelled","client_aborted":true}
		}`), &call); err != nil {
			t.Fatal(err)
		}
		if !call.ClientAborted || !call.Aborted() {
			t.Fatalf("typed abort: %+v", call)
		}
		if call.AbortPhase != AbortPhaseBeforeHeaders {
			t.Fatalf("AbortPhase=%q", call.AbortPhase)
		}
		if call.Normalized.FinishReason != "cancelled" {
			t.Fatalf("finish=%q", call.Normalized.FinishReason)
		}
	})

	t.Run("normalized_alias", func(t *testing.T) {
		var call Call
		if err := json.Unmarshal([]byte(`{
			"id":"call_a",
			"seq":1,
			"http_status":0,
			"normalized":{"finish_reason":"cancelled","client_aborted":true}
		}`), &call); err != nil {
			t.Fatal(err)
		}
		if !call.ClientAborted || !call.Aborted() {
			t.Fatalf("alias should promote to ClientAborted: %+v", call)
		}
		if call.AbortPhase != "" {
			t.Fatalf("old proxy has no phase, got %q", call.AbortPhase)
		}
	})

	t.Run("after_headers", func(t *testing.T) {
		var call Call
		if err := json.Unmarshal([]byte(`{
			"id":"call_2",
			"seq":1,
			"http_status":200,
			"client_aborted":true,
			"abort_phase":"after_headers",
			"normalized":{"finish_reason":"cancelled","client_aborted":true,"output_text":"partial"}
		}`), &call); err != nil {
			t.Fatal(err)
		}
		if !call.Aborted() || call.AbortPhase != AbortPhaseAfterHeaders || call.HTTPStatus != 200 {
			t.Fatalf("after_headers abort: %+v", call)
		}
	})

	t.Run("normal_call", func(t *testing.T) {
		var call Call
		if err := json.Unmarshal([]byte(`{
			"id":"call_3",
			"seq":1,
			"http_status":200,
			"client_aborted":false,
			"normalized":{"finish_reason":"stop"}
		}`), &call); err != nil {
			t.Fatal(err)
		}
		if call.Aborted() || call.AbortPhase != "" {
			t.Fatalf("normal call should not be aborted: %+v", call)
		}
	})

	t.Run("done_close_is_not_abort", func(t *testing.T) {
		var call Call
		if err := json.Unmarshal([]byte(`{
			"id":"call_done",
			"seq":1,
			"http_status":200,
			"stream":true,
			"normalized":{"finish_reason":"stop","output_text":"hi"}
		}`), &call); err != nil {
			t.Fatal(err)
		}
		if call.ClientAborted || call.Aborted() || call.AbortPhase != "" {
			t.Fatalf("close after [DONE] is not an abort: %+v", call)
		}
		encoded, err := json.Marshal(call)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "abort_phase") || strings.Contains(string(encoded), "client_aborted") {
			t.Fatalf("abort fields must be omitted on a finished stream: %s", encoded)
		}
	})

	t.Run("phase_omitted_when_not_aborted", func(t *testing.T) {
		var call Call
		if err := json.Unmarshal([]byte(`{
			"id":"call_omit",
			"seq":1,
			"http_status":200,
			"client_aborted":false,
			"abort_phase":"after_headers",
			"normalized":{"finish_reason":"stop"}
		}`), &call); err != nil {
			t.Fatal(err)
		}
		if call.Aborted() || call.AbortPhase != "" {
			t.Fatalf("abort_phase must be omitted when not aborted: %+v", call)
		}
	})

	t.Run("unknown_phase_falls_back_to_normalized", func(t *testing.T) {
		var call Call
		if err := json.Unmarshal([]byte(`{
			"id":"call_bad",
			"seq":1,
			"client_aborted":true,
			"abort_phase":"mid_stream",
			"normalized":{"finish_reason":"cancelled","client_aborted":true,"raw":{"abort_phase":"before_headers"}}
		}`), &call); err != nil {
			t.Fatal(err)
		}
		if !call.Aborted() || call.AbortPhase != AbortPhaseBeforeHeaders {
			t.Fatalf("unknown top-level phase should fall back: %+v", call)
		}
	})

	t.Run("responses_completed_is_not_abort", func(t *testing.T) {
		var call Call
		if err := json.Unmarshal([]byte(`{
			"id":"call_resp",
			"seq":1,
			"http_status":200,
			"stream":true,
			"api_family":"responses",
			"sse_events":[{"event":"response.completed"}],
			"normalized":{"finish_reason":"completed","raw":{"phase":"after_headers"}}
		}`), &call); err != nil {
			t.Fatal(err)
		}
		if call.ClientAborted || call.Aborted() || call.AbortPhase != "" {
			t.Fatalf("close after a terminal Responses event is not an abort: %+v", call)
		}
	})
}

func TestAssertionsAborted(t *testing.T) {
	aborted := Trace{SessionID: "s", Calls: []Call{{
		ID:            "call_1",
		ClientAborted: true,
		AbortPhase:    AbortPhaseBeforeHeaders,
		Normalized:    Normalized{FinishReason: "cancelled", ClientAborted: true},
	}}}
	if !aborted.Query().Check(t).Aborted() {
		t.Fatal("Aborted should pass")
	}

	tb := &fakeTB{}
	normal := Trace{SessionID: "s", Calls: []Call{{ID: "call_2", HTTPStatus: 200}}}
	if normal.Query().Check(tb).Aborted() {
		t.Fatal("Aborted should fail on a normal call")
	}
	if !strings.Contains(strings.Join(tb.messages, "\n"), "client-aborted") {
		t.Fatalf("message: %v", tb.messages)
	}
}
