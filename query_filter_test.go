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

func TestOffersToolAndWithoutTools(t *testing.T) {
	t.Parallel()
	trace := goldenMultiCallTrace()
	router := trace.Query().WithoutTools().Calls()
	if len(router) != 1 || router[0].Seq != 1 {
		t.Fatalf("without_tools=%v", ids(router))
	}
	specialist := trace.Query().OffersTool("get_risks").Calls()
	if len(specialist) != 2 || specialist[0].Seq != 2 || specialist[1].Seq != 3 {
		t.Fatalf("offers_tool get_risks=%v", ids(specialist))
	}
	follow := trace.Query().OffersTool("get_risks").Since(Mark{Seq: 2}).Calls()
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
	if !router.Expect(t).ToolNotCalled("get_risks") {
		t.Fatal("router turn should not call get_risks")
	}
	if !router.Expect(t).CallCount(1) {
		t.Fatal("router CallCount(1) should pass")
	}
	specialist := trace.Query().OffersTool("get_risks")
	if !specialist.Expect(t).CallCount(2) {
		t.Fatal("offers_tool CallCount(2) should pass")
	}
	if !specialist.Expect(t).ToolCalled("get_risks") {
		t.Fatal("offers_tool ToolCalled should pass")
	}
}

func goldenMultiCallTrace() Trace {
	six := []ToolDecl{
		{Name: "get_risks"}, {Name: "get_user_health_tips"}, {Name: "get_labs"},
		{Name: "get_meds"}, {Name: "get_sleep"}, {Name: "get_user_biological_age"},
	}
	return Trace{SessionID: "s", Calls: []Call{
		{
			Seq: 1,
			Normalized: Normalized{
				FinishReason: "stop",
				OutputText:   `{"route":"specialist"}`,
			},
			RawRequest: []byte(`{"response_format":{"type":"json_schema"},"messages":[{"role":"user","content":"route me"}]}`),
		},
		{
			Seq:        2,
			Normalized: Normalized{ToolsDeclared: six, ToolCalls: []ToolCall{{ID: "c1", Name: "get_risks"}}},
		},
		{
			Seq:        3,
			Normalized: Normalized{ToolsDeclared: six, ToolResultsIn: []ToolResult{{CallID: "c1", Name: "get_risks"}}},
			RawRequest: []byte(`{"messages":[{"role":"tool","tool_call_id":"c1","content":"SEED_Cardiovascular_Risk"}]}`),
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
