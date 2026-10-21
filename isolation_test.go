// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCallSeqFromWireAndSummaryShowsGlobalID(t *testing.T) {
	var call Call
	if err := json.Unmarshal([]byte(`{"id":"call_3","seq":1,"session_id":"weather"}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.Seq != 1 || call.ID != "call_3" || call.SessionID != "weather" {
		t.Fatalf("wire call = %+v", call)
	}
	summary := Trace{SessionID: "weather", Calls: []Call{call}}.Summary()
	if !strings.Contains(summary, "#1 (call_3)") {
		t.Fatalf("summary missing #1 (call_3):\n%s", summary)
	}
}

func TestUserTextsChatAndResponses(t *testing.T) {
	chat := Call{RawRequest: []byte(`{
		"messages":[
			{"role":"system","content":"Never mention Amsterdam weather or get_current_weather."},
			{"role":"user","content":"Start the barge meeting"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":[{"type":"text","text":"Second turn"}]}
		]
	}`)}
	got := chat.UserTexts()
	if len(got) != 2 || got[0] != "Start the barge meeting" || got[1] != "Second turn" {
		t.Fatalf("chat UserTexts=%v", got)
	}

	responses := Call{RawRequest: []byte(`{
		"instructions":"Never mention Amsterdam weather or get_current_weather.",
		"input":[
			{"role":"developer","content":"hidden"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Weather in Amsterdam?"}]}
		]
	}`)}
	got = responses.UserTexts()
	if len(got) != 1 || got[0] != "Weather in Amsterdam?" {
		t.Fatalf("responses UserTexts=%v", got)
	}

	stringInput := Call{RawRequest: []byte(`{"instructions":"system","input":"Name a primary color"}`)}
	got = stringInput.UserTexts()
	if len(got) != 1 || got[0] != "Name a primary color" {
		t.Fatalf("string input UserTexts=%v", got)
	}
}

func TestIsolatedFromFixtures(t *testing.T) {
	barge := isolationCall("barge", "call_1", 1, "path", `{
		"messages":[
			{"role":"system","content":"Never mention Amsterdam weather or get_current_weather."},
			{"role":"user","content":"Start the barge meeting"}
		],
		"tools":[{"function":{"name":"schedule_barge"}}]
	}`)
	weather := isolationCall("weather", "call_3", 1, "path", `{
		"messages":[
			{"role":"system","content":"You are a weather bot. Use get_current_weather."},
			{"role":"user","content":"Weather in Amsterdam?"}
		],
		"tools":[{"function":{"name":"get_current_weather"}}]
	}`)
	bargeTrace := Trace{SessionID: "barge", Calls: []Call{barge}}
	weatherTrace := Trace{SessionID: "weather", Calls: []Call{weather}}

	t.Run("isolated", func(t *testing.T) {
		tb := &fakeTB{}
		a := bargeTrace.Query().Check(tb)
		if !a.IsolatedFrom("weather", WithOtherTrace(weatherTrace), RequirePathSource()) {
			t.Fatalf("isolated sessions should pass: %v", tb.messages)
		}
		if tb.failed {
			t.Fatalf("unexpected errors: %v", tb.messages)
		}
	})

	t.Run("system_mentions_other_tool", func(t *testing.T) {
		tb := &fakeTB{}
		a := bargeTrace.Query().Check(tb)
		if !a.IsolatedFrom("weather", WithOtherTrace(weatherTrace)) {
			t.Fatalf("system prompt naming the other tool must not fail: %v", tb.messages)
		}
	})

	t.Run("shared_user_text", func(t *testing.T) {
		leaked := isolationCall("barge", "call_1", 1, "path", `{
			"messages":[
				{"role":"system","content":"Never mention Amsterdam weather."},
				{"role":"user","content":"Please ignore: Weather in Amsterdam?"}
			]
		}`)
		tb := &fakeTB{}
		a := Trace{SessionID: "barge", Calls: []Call{leaked}}.Query().Check(tb)
		if a.IsolatedFrom("weather", WithOtherTrace(weatherTrace)) {
			t.Fatal("shared user text should fail")
		}
		joined := strings.Join(tb.messages, "\n")
		for _, want := range []string{"user text", "Weather in Amsterdam?", "call_1", "call_3", "barge", "weather"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q in:\n%s", want, joined)
			}
		}
	})

	t.Run("foreign_session_id", func(t *testing.T) {
		foreign := barge
		foreign.SessionID = "weather"
		tb := &fakeTB{}
		a := Trace{SessionID: "barge", Calls: []Call{foreign}}.Query().Check(tb)
		if a.IsolatedFrom("weather", WithOtherTrace(weatherTrace)) {
			t.Fatal("foreign session_id should fail")
		}
		joined := strings.Join(tb.messages, "\n")
		if !strings.Contains(joined, "session_id=weather") || !strings.Contains(joined, "want barge") {
			t.Fatalf("foreign session_id message:\n%s", joined)
		}

		otherForeign := weather
		otherForeign.SessionID = "barge"
		tb2 := &fakeTB{}
		a2 := bargeTrace.Query().Check(tb2)
		if a2.IsolatedFrom("weather", WithOtherTrace(Trace{SessionID: "weather", Calls: []Call{otherForeign}})) {
			t.Fatal("foreign session_id on the other trace should fail")
		}
		joined2 := strings.Join(tb2.messages, "\n")
		if !strings.Contains(joined2, "session_id=barge") || !strings.Contains(joined2, "want weather") {
			t.Fatalf("other-trace foreign session_id message:\n%s", joined2)
		}
	})

	t.Run("empty_fails_unless_allow_empty", func(t *testing.T) {
		empty := Trace{SessionID: "empty"}
		tb := &fakeTB{}
		if empty.Query().Check(tb).IsolatedFrom("weather", WithOtherTrace(weatherTrace)) {
			t.Fatal("empty session should fail")
		}
		if !strings.Contains(strings.Join(tb.messages, "\n"), "no calls recorded for session empty") {
			t.Fatalf("empty message: %v", tb.messages)
		}

		ok := &fakeTB{}
		if !empty.Query().AllowEmpty().Check(ok).IsolatedFrom("weather", WithOtherTrace(weatherTrace)) {
			t.Fatalf("AllowEmpty should pass: %v", ok.messages)
		}
		if ok.failed {
			t.Fatalf("AllowEmpty recorded errors: %v", ok.messages)
		}
	})

	t.Run("shared_call_id", func(t *testing.T) {
		dup := weather
		dup.ID = "call_1"
		tb := &fakeTB{}
		a := bargeTrace.Query().Check(tb)
		if a.IsolatedFrom("weather", WithOtherTrace(Trace{SessionID: "weather", Calls: []Call{dup}})) {
			t.Fatal("shared call id should fail")
		}
		joined := strings.Join(tb.messages, "\n")
		if !strings.Contains(joined, "call id call_1 appears in sessions barge and weather") {
			t.Fatalf("shared id message:\n%s", joined)
		}
	})
}

func isolationCall(sessionID, id string, seq int, source, raw string) Call {
	return Call{
		ID:            id,
		SessionID:     sessionID,
		SessionSource: source,
		Seq:           seq,
		RawRequest:    []byte(raw),
	}
}
