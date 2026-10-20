// SPDX-License-Identifier: MIT

package llmentary

import (
	"strings"
	"testing"
)

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
