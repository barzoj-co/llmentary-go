// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func outcomeVectorCases(t *testing.T) []outcomeVectorCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "control", "call_outcomes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Version int                 `json:"version"`
		Cases   []outcomeVectorCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	if vector.Version != 1 {
		t.Fatalf("version=%d want 1", vector.Version)
	}
	if len(vector.Cases) != 15 {
		t.Fatalf("cases=%d want 15", len(vector.Cases))
	}
	return vector.Cases
}

func outcomeVectorCall(t *testing.T, tc outcomeVectorCase) Call {
	t.Helper()
	var call Call
	if err := json.Unmarshal(tc.Call, &call); err != nil {
		t.Fatal(err)
	}
	return call
}

func outcomeAssertions(call Call, tb *fakeTB) *Assertions {
	return &Assertions{t: tb, trace: Trace{SessionID: "s", Calls: []Call{call}}}
}

func wantNoErrorsFailure(call Call) string {
	errorName := "-"
	if call.Normalized.Error != nil && call.Normalized.Error.Normalized != "" {
		errorName = call.Normalized.Error.Normalized
	}
	return fmt.Sprintf("unexpected failed call #%d (outcome=failed, error=%s, http_status=%d)",
		call.Seq, errorName, call.HTTPStatus)
}

func wantNoCancelledFailure(call Call) string {
	phase := call.AbortPhase
	if phase == "" {
		phase = "-"
	}
	return fmt.Sprintf("unexpected cancelled call #%d (abort_phase=%s)", call.Seq, phase)
}

func TestNoErrorsOutcomeVector(t *testing.T) {
	for _, tc := range outcomeVectorCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			call := outcomeVectorCall(t, tc)
			wantPass := tc.Outcome == OutcomeCommitted || tc.Outcome == OutcomeCancelled
			tb := &fakeTB{}
			got := outcomeAssertions(call, tb).NoErrors()
			if got != wantPass {
				t.Fatalf("NoErrors()=%v want %v for outcome=%q: %v", got, wantPass, tc.Outcome, tb.messages)
			}
			tb.flush()
			joined := strings.Join(tb.messages, "\n")
			if wantPass {
				if tb.failed {
					t.Fatalf("NoErrors should pass for outcome=%q: %s", tc.Outcome, joined)
				}
				return
			}
			if !strings.Contains(joined, wantNoErrorsFailure(call)) {
				t.Fatalf("NoErrors failure text mismatch for outcome=%q:\n%s", tc.Outcome, joined)
			}
		})
	}
}

func TestNoCancelledOutcomeVector(t *testing.T) {
	for _, tc := range outcomeVectorCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			call := outcomeVectorCall(t, tc)
			wantPass := tc.Outcome != OutcomeCancelled
			tb := &fakeTB{}
			got := outcomeAssertions(call, tb).NoCancelled()
			if got != wantPass {
				t.Fatalf("NoCancelled()=%v want %v for outcome=%q: %v", got, wantPass, tc.Outcome, tb.messages)
			}
			tb.flush()
			joined := strings.Join(tb.messages, "\n")
			if wantPass {
				if tb.failed {
					t.Fatalf("NoCancelled should pass for outcome=%q: %s", tc.Outcome, joined)
				}
				return
			}
			if !strings.Contains(joined, wantNoCancelledFailure(call)) {
				t.Fatalf("NoCancelled failure text mismatch for outcome=%q:\n%s", tc.Outcome, joined)
			}
		})
	}
}

func TestNoErrorsAcceptsClientCancelsInBothPhases(t *testing.T) {
	byName := map[string]Call{}
	for _, tc := range outcomeVectorCases(t) {
		byName[tc.Name] = outcomeVectorCall(t, tc)
	}
	passCases := []string{
		"before_headers_abort_source_client",
		"before_headers_abort_old_proxy_no_source",
		"after_headers_abort_mid_stream",
	}
	failCases := []string{
		"malformed_client_aborted_error_without_flag",
		"aborted_with_upstream_error",
		"upstream_drop_truncated",
	}
	mixed := Trace{SessionID: "s", Calls: []Call{
		byName[passCases[0]], byName[passCases[1]], byName[passCases[2]],
	}}
	if !mixed.Query().Check(t).NoErrors() {
		t.Fatal("client cancels in both phases must pass NoErrors")
	}
	cancelTB := &fakeTB{}
	if mixed.Query().Check(cancelTB).NoCancelled() {
		t.Fatal("NoCancelled should fail on cancelled calls")
	}
	cancelTB.flush()
	if joined := strings.Join(cancelTB.messages, "\n"); !strings.Contains(joined, wantNoCancelledFailure(byName[passCases[0]])) {
		t.Fatalf("NoCancelled failure text:\n%s", joined)
	}
	for _, name := range failCases {
		tb := &fakeTB{}
		if outcomeAssertions(byName[name], tb).NoErrors() {
			t.Fatalf("%s must fail NoErrors", name)
		}
		tb.flush()
		if joined := strings.Join(tb.messages, "\n"); !strings.Contains(joined, wantNoErrorsFailure(byName[name])) {
			t.Fatalf("%s failure text:\n%s", name, joined)
		}
	}
}
