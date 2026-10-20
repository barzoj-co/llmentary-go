// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMentionsAnyTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		text       string
		candidates []string
		want       bool
	}{
		{"degree compact", "it is 20°C outside", []string{"20 °C"}, true},
		{"degree spaced", "Temperature: 20 °C", []string{"20°C"}, true},
		{"degrees word", "about 20 degrees today", []string{"20°C"}, true},
		{"casefold", "Moderate cardiovascular risk", []string{"moderate"}, true},
		{"no match", "the sky is blue", []string{"20", "sunny"}, false},
		{"number inside larger", "It is 120 °C", []string{"20"}, false},
		{"year 2020", "in 2020", []string{"20"}, false},
		{"about vs abc", "about", []string{"abc"}, false},
		{"abc vs about", "abc", []string{"about"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MentionsAny(tc.text, tc.candidates...)
			if got != tc.want {
				t.Fatalf("MentionsAny(%q, %v)=%v want %v", tc.text, tc.candidates, got, tc.want)
			}
			if !tc.want {
				msg := mentionMissMessage(tc.text, tc.candidates)
				for _, c := range tc.candidates {
					if !strings.Contains(msg, c) {
						t.Fatalf("failure missing candidate %q: %s", c, msg)
					}
				}
				if !strings.Contains(msg, "excerpt:") {
					t.Fatalf("failure missing excerpt: %s", msg)
				}
			}
		})
	}
}

func TestMentionMissExcerptLimit(t *testing.T) {
	text := strings.Repeat("x", 250)
	msg := mentionMissMessage(text, []string{"nope"})
	excerpt := text[:mentionExcerptLimit]
	if !strings.Contains(msg, excerpt) || strings.Contains(msg, text) {
		t.Fatalf("excerpt should be capped at %d: %s", mentionExcerptLimit, msg)
	}
}

func TestFinalResponseMentionsAnyAndToolResultInPrompt(t *testing.T) {
	result := `{"temperature":20,"condition":"sunny","token":"SEED_X"}`
	clean := Trace{SessionID: "s", Calls: []Call{
		{
			Seq: 1,
			Normalized: Normalized{
				ToolCalls:  []ToolCall{{ID: "call_w", Name: "get_weather"}},
				OutputText: "",
			},
		},
		{
			Seq:        2,
			RawRequest: []byte(`{"messages":[{"role":"user","content":"weather?"},{"role":"tool","tool_call_id":"call_w","content":` + jsonQuote(result) + `}]}`),
			Normalized: Normalized{
				ToolResultsIn: []ToolResult{{CallID: "call_w", Name: "get_weather"}},
				OutputText:    "It is 20 °C and sunny.",
			},
		},
	}}
	a := &Assertions{t: t, trace: clean, fatal: true}
	a.ToolResultInPrompt("get_weather")
	a.PromptContains("SEED_X")
	a.FinalResponseMentionsAny("20", "sunny")

	mutated := clean
	mutated.Calls = append([]Call(nil), clean.Calls...)
	mutated.Calls[1].RawRequest = []byte(`{"messages":[{"role":"user","content":"weather?"}]}`)
	mutated.Calls[1].Normalized.OutputText = "I cannot say."
	tb := &fakeTB{}
	fail := &Assertions{t: tb, trace: mutated}
	fail.ToolResultInPrompt("get_weather")
	if !tb.failed || len(tb.messages) != 1 {
		t.Fatalf("ToolResultInPrompt should fail independently: messages=%q", tb.messages)
	}
	fail.FinalResponseMentionsAny("20", "sunny")
	if len(tb.messages) != 2 {
		t.Fatalf("FinalResponseMentionsAny should fail independently: messages=%q", tb.messages)
	}
	joined := strings.Join(tb.messages, "\n")
	if !strings.Contains(joined, "get_weather") || !strings.Contains(joined, "20") {
		t.Fatalf("messages=%q", joined)
	}
}

func TestPromptTextSystemAndInstructions(t *testing.T) {
	call := Call{RawRequest: []byte(`{
		"system":[{"type":"text","text":"SYS_BLOCK"}],
		"instructions":"INSTR_TEXT",
		"messages":[{"role":"user","content":"hi"}]
	}`)}
	got := call.PromptText()
	for _, want := range []string{"SYS_BLOCK", "INSTR_TEXT", "hi"} {
		if !strings.Contains(got, want) {
			t.Fatalf("PromptText missing %q: %q", want, got)
		}
	}
}

func TestFindToolResultNestedToolUseID(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"use_1","content":"NESTED_RESULT"}]}]}`)
	if got := toolResultContent(raw, "use_1", "weather"); got != "NESTED_RESULT" {
		t.Fatalf("got %q", got)
	}
}

func TestSoftModeReportsAllFailures(t *testing.T) {
	tb := &fakeTB{}
	a := &Assertions{t: tb, trace: Trace{SessionID: "s", Calls: []Call{{
		Seq:        1,
		Normalized: Normalized{OutputText: "nothing useful"},
	}}}}
	a.ToolCalled("missing")
	a.PromptContains("SEED_X")
	a.FinalResponseMentionsAny("20", "sunny")
	if tb.fatal {
		t.Fatal("Check/soft should not Fatal")
	}
	if len(tb.messages) < 3 {
		t.Fatalf("want 3 failures, got %d: %q", len(tb.messages), tb.messages)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
