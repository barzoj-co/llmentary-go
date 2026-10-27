// SPDX-License-Identifier: MIT

package llmentary

import (
	"strings"
	"testing"
)

func TestQueryCommittedAndAborted(t *testing.T) {
	t.Parallel()
	retry := 1
	trace := Trace{SessionID: "mixed", Calls: []Call{
		{ID: "ok", Seq: 1, HTTPStatus: 200},
		{ID: "barge", Seq: 2, HTTPStatus: 200, ClientAborted: true, AbortPhase: AbortPhaseAfterHeaders},
		{ID: "retry", Seq: 3, RetryCount: &retry, HTTPStatus: 200},
		{ID: "alias", Seq: 4, HTTPStatus: 0, Normalized: Normalized{ClientAborted: true}},
	}}
	committed := trace.Query().Committed().Calls()
	if len(committed) != 2 || committed[0].ID != "ok" || committed[1].ID != "retry" {
		t.Fatalf("committed=%v", ids(committed))
	}
	aborted := trace.Query().Aborted().Calls()
	if len(aborted) != 2 || aborted[0].ID != "barge" || aborted[1].ID != "alias" {
		t.Fatalf("aborted=%v", ids(aborted))
	}
	if trace.Query().Status(200).Committed().Len() != 2 {
		t.Fatal("Status(200) still includes after-headers abort; Committed must drop it")
	}
	tb := &fakeTB{}
	if !trace.Query().Committed().Check(tb).CallCount(2) {
		t.Fatal("Committed().CallCount(2) should pass")
	}
	if !trace.Query().Check(tb).CallCount(2, ExcludeAborted()) {
		t.Fatal("CallCount(2, ExcludeAborted) should pass")
	}
}

func TestQueryOutcomeFiltersOnVector(t *testing.T) {
	t.Parallel()
	cases := outcomeVectorCases(t)
	calls := make([]Call, 0, len(cases))
	wantSeqs := map[string][]int{}
	for _, tc := range cases {
		call := outcomeVectorCall(t, tc)
		calls = append(calls, call)
		wantSeqs[tc.Outcome] = append(wantSeqs[tc.Outcome], call.Seq)
	}
	trace := Trace{SessionID: "vector", Calls: calls}

	cancelled := trace.Query().Cancelled().Calls()
	if len(cancelled) != 3 || !sameSeqs(ids(cancelled), wantSeqs[OutcomeCancelled]) {
		t.Fatalf("cancelled=%v want %v", ids(cancelled), wantSeqs[OutcomeCancelled])
	}
	failed := trace.Query().Failed().Calls()
	if len(failed) != 9 || !sameSeqs(ids(failed), wantSeqs[OutcomeFailed]) {
		t.Fatalf("failed=%v want %v", ids(failed), wantSeqs[OutcomeFailed])
	}
	strict := trace.Query().Outcome(OutcomeCommitted).Calls()
	if len(strict) != 3 || !sameSeqs(ids(strict), wantSeqs[OutcomeCommitted]) {
		t.Fatalf("outcome(committed)=%v want %v", ids(strict), wantSeqs[OutcomeCommitted])
	}
	both := trace.Query().Outcome(OutcomeCancelled, OutcomeFailed).Calls()
	if len(both) != len(wantSeqs[OutcomeCancelled])+len(wantSeqs[OutcomeFailed]) {
		t.Fatalf("outcome(cancelled,failed)=%v", ids(both))
	}
	committed := trace.Query().Committed().Calls()
	aborted, nonAbortedFailed := 0, 0
	for _, call := range calls {
		if call.Aborted() {
			aborted++
		}
		if call.Outcome() == OutcomeFailed && !call.Aborted() {
			nonAbortedFailed++
		}
	}
	if len(committed) != 11 || len(committed) != len(calls)-aborted {
		t.Fatalf("committed=%d want %d (not client-aborted): %v", len(committed), len(calls)-aborted, ids(committed))
	}
	if got := trace.Query().Committed().Failed().Len(); got != 8 || got != nonAbortedFailed {
		t.Fatalf("Committed() must still keep failed calls: got %d want %d", got, nonAbortedFailed)
	}
}

func TestQueryOutcomeFilterLabelsInEmptyFailures(t *testing.T) {
	t.Parallel()
	trace := Trace{SessionID: "vector", Calls: []Call{
		{ID: "ok", Seq: 1, HTTPStatus: 200, Normalized: Normalized{FinishReason: "stop"}},
	}}
	cases := []struct {
		name   string
		query  Query
		wantIn []string
	}{
		{name: "Cancelled", query: trace.Query().Cancelled(), wantIn: []string{"no calls matched query", "cancelled", "session has 1 calls"}},
		{name: "Failed", query: trace.Query().Failed(), wantIn: []string{"no calls matched query", "failed", "session has 1 calls"}},
		{name: "Outcome", query: trace.Query().Outcome(OutcomeCancelled, OutcomeFailed), wantIn: []string{"no calls matched query", "outcome(cancelled,failed)", "session has 1 calls"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := &fakeTB{}
			if tc.query.Check(tb).NoErrors() {
				t.Fatalf("empty %s filter NoErrors should fail", tc.name)
			}
			tb.flush()
			joined := strings.Join(tb.messages, "\n")
			for _, want := range tc.wantIn {
				if !strings.Contains(joined, want) {
					t.Fatalf("missing %q in:\n%s", want, joined)
				}
			}
		})
	}
}

func TestOffersToolAndWithoutTools(t *testing.T) {
	t.Parallel()
	trace := goldenMultiCallTrace()
	router := trace.Query().WithoutTools().Calls()
	if len(router) != 1 || router[0].Seq != 1 {
		t.Fatalf("without_tools=%v", ids(router))
	}
	specialist := trace.Query().OffersTool("get_order_status").Calls()
	if len(specialist) != 2 || specialist[0].Seq != 2 || specialist[1].Seq != 3 {
		t.Fatalf("offers_tool get_order_status=%v", ids(specialist))
	}
	follow := trace.Query().OffersTool("get_order_status").Since(Mark{Seq: 2}).Calls()
	if len(follow) != 1 || follow[0].Seq != 3 {
		t.Fatalf("follow-up since mark=%v", ids(follow))
	}
}

func TestSinceFiltersExpect(t *testing.T) {
	trace := goldenMultiCallTrace()
	got := trace.Query().Since(Mark{Seq: 2}).Calls()
	if len(got) != 1 || got[0].Seq != 3 {
		t.Fatalf("since(2)=%v", ids(got))
	}
	summary := trace.Summary()
	for _, want := range []string{"#1", "#2", "#3"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestEmptyFilteredNoErrorsAndToolNotCalledFail(t *testing.T) {
	trace := goldenMultiCallTrace()
	cases := []struct {
		name   string
		query  Query
		wantIn []string
	}{
		{
			name:   "without_tools after tool turns",
			query:  trace.Query().Since(Mark{Seq: 1}).WithoutTools(),
			wantIn: []string{"no calls matched query", "without_tools", "since=1", "session has 3 calls"},
		},
		{
			name:   "offers_tool missing",
			query:  trace.Query().OffersTool("missing"),
			wantIn: []string{"no calls matched query", `offers_tool("missing")`, "session has 3 calls"},
		},
		{
			name:   "since last",
			query:  trace.Query().Since(Mark{Seq: 3}),
			wantIn: []string{"no calls matched query", "since=3", "session has 3 calls"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/NoErrors", func(t *testing.T) {
			tb := &fakeTB{}
			if tc.query.Check(tb).NoErrors() {
				t.Fatal("empty filter NoErrors should fail")
			}
			tb.flush()
			joined := strings.Join(tb.messages, "\n")
			for _, want := range tc.wantIn {
				if !strings.Contains(joined, want) {
					t.Fatalf("missing %q in:\n%s", want, joined)
				}
			}
		})
		t.Run(tc.name+"/ToolNotCalled", func(t *testing.T) {
			tb := &fakeTB{}
			if tc.query.Check(tb).ToolNotCalled("x") {
				t.Fatal("empty filter ToolNotCalled should fail")
			}
			tb.flush()
			joined := strings.Join(tb.messages, "\n")
			for _, want := range tc.wantIn {
				if !strings.Contains(joined, want) {
					t.Fatalf("missing %q in:\n%s", want, joined)
				}
			}
		})
	}
}

func TestEmptyFilteredAllowEmptyPasses(t *testing.T) {
	trace := goldenMultiCallTrace()
	empty := trace.Query().OffersTool("missing").Since(Mark{Seq: 3})
	tb := &fakeTB{}
	if !empty.AllowEmpty().Check(tb).NoErrors() {
		t.Fatalf("Query.AllowEmpty NoErrors failed: %v", tb.messages)
	}
	if !empty.Check(tb, AllowEmpty()).ToolNotCalled("x") {
		t.Fatalf("AllowEmpty() ToolNotCalled failed: %v", tb.messages)
	}
	if tb.failed {
		t.Fatalf("AllowEmpty should not record errors: %v", tb.messages)
	}
}

func TestNonEmptyFilterAssertionsUnchanged(t *testing.T) {
	trace := goldenMultiCallTrace()
	router := trace.Query().WithoutTools()
	if !router.Expect(t).NoErrors() {
		t.Fatal("non-empty without_tools NoErrors should pass")
	}
	if !router.Expect(t).ToolNotCalled("get_order_status") {
		t.Fatal("router turn should not call get_order_status")
	}
	if !router.Expect(t).CallCount(1) {
		t.Fatal("router CallCount(1) should pass")
	}
	specialist := trace.Query().OffersTool("get_order_status")
	if !specialist.Expect(t).CallCount(2) {
		t.Fatal("offers_tool CallCount(2) should pass")
	}
	if !specialist.Expect(t).ToolCalled("get_order_status") {
		t.Fatal("offers_tool ToolCalled should pass")
	}
}

func goldenMultiCallTrace() Trace {
	six := []ToolDecl{
		{Name: "get_order_status"}, {Name: "get_shipping_options"}, {Name: "get_invoices"},
		{Name: "get_returns"}, {Name: "get_addresses"}, {Name: "get_account_profile"},
	}
	return Trace{SessionID: "s", Calls: []Call{
		{
			Seq:        1,
			HTTPStatus: 200,
			Normalized: Normalized{
				FinishReason: "stop",
				OutputText:   `{"route":"specialist"}`,
			},
			RawRequest: []byte(`{"response_format":{"type":"json_schema"},"messages":[{"role":"user","content":"route me"}]}`),
		},
		{
			Seq:        2,
			HTTPStatus: 200,
			Normalized: Normalized{ToolsDeclared: six, ToolCalls: []ToolCall{{ID: "c1", Name: "get_order_status"}}},
		},
		{
			Seq:        3,
			HTTPStatus: 200,
			Normalized: Normalized{ToolsDeclared: six, ToolResultsIn: []ToolResult{{CallID: "c1", Name: "get_order_status"}}},
			RawRequest: []byte(`{"messages":[{"role":"tool","tool_call_id":"c1","content":"SEED_Order_Status"}]}`),
		},
	}}
}

func ids(calls []Call) []int {
	out := make([]int, len(calls))
	for i, c := range calls {
		out[i] = c.Seq
	}
	return out
}

func sameSeqs(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
