// SPDX-License-Identifier: MIT

package llmentary

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestNormalizeIsolationTextCasefold(t *testing.T) {
	strasse := normalizeIsolationText("DIE STRASSE IST NASS HEUTE")
	eszett := normalizeIsolationText("Die Straße ist nass heute")
	if strasse == "" || strasse != eszett {
		t.Fatalf("eszett fold: %q vs %q", strasse, eszett)
	}
	capital := normalizeIsolationText("replay \u039f\u0394\u039f\u03a3")    // ΟΔΟΣ
	finalSigma := normalizeIsolationText("replay \u03bf\u03b4\u03bf\u03c2") // οδος
	if capital == "" || capital != finalSigma {
		t.Fatalf("greek sigma fold: %q vs %q", capital, finalSigma)
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

	t.Run("same_canned_question", func(t *testing.T) {
		prompt := `{
			"messages":[
				{"role":"system","content":"You are a health assistant. Use get_bioage."},
				{"role":"user","content":"What is my biological age?"}
			]
		}`
		left := isolationCall("a", "call_a", 1, "path", prompt)
		right := isolationCall("b", "call_b", 1, "path", prompt)
		tb := &fakeTB{}
		a := Trace{SessionID: "a", Calls: []Call{left}}.Query().Check(tb)
		if !a.IsolatedFrom("b", WithOtherTrace(Trace{SessionID: "b", Calls: []Call{right}})) {
			t.Fatalf("identical canned questions should pass: %v", tb.messages)
		}
	})

	t.Run("short_hi_not_substring", func(t *testing.T) {
		hi := isolationCall("a", "call_hi", 1, "", `{"messages":[{"role":"user","content":"Hi"}]}`)
		long := isolationCall("b", "call_long", 1, "", `{"messages":[{"role":"user","content":"Hi, what is my biological age? This text is long."}]}`)
		tb := &fakeTB{}
		a := Trace{SessionID: "a", Calls: []Call{hi}}.Query().Check(tb)
		if !a.IsolatedFrom("b", WithOtherTrace(Trace{SessionID: "b", Calls: []Call{long}})) {
			t.Fatalf("Hi vs longer text should pass: %v", tb.messages)
		}
	})

	t.Run("history_replay", func(t *testing.T) {
		origin := isolationCall("a", "call_a", 1, "", `{
			"messages":[
				{"role":"system","content":"You are a health assistant."},
				{"role":"user","content":"What is my biological age?"}
			]
		}`)
		replay := isolationCall("b", "call_b", 1, "", `{
			"messages":[
				{"role":"system","content":"You are a health assistant."},
				{"role":"user","content":"What is my biological age?"},
				{"role":"assistant","content":"42"},
				{"role":"user","content":"And what about my BMI?"}
			]
		}`)
		tb := &fakeTB{}
		a := Trace{SessionID: "b", Calls: []Call{replay}}.Query().Check(tb)
		if a.IsolatedFrom("a", WithOtherTrace(Trace{SessionID: "a", Calls: []Call{origin}})) {
			t.Fatal("history replay should fail")
		}
		joined := strings.Join(tb.messages, "\n")
		for _, want := range []string{
			"user text 'What is my biological age?'",
			"also introduced by session a #1 (call_a)",
			"appears in session b #1 (call_b)",
		} {
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

	t.Run("empty_other_fails_unless_allow_empty", func(t *testing.T) {
		empty := Trace{SessionID: "ghost"}
		tb := &fakeTB{}
		if bargeTrace.Query().Check(tb).IsolatedFrom("ghost", WithOtherTrace(empty)) {
			t.Fatal("empty other session should fail")
		}
		joined := strings.Join(tb.messages, "\n")
		if !strings.Contains(joined, "no calls recorded for session ghost") ||
			!strings.Contains(joined, "isolated_from needs calls on both sides") {
			t.Fatalf("empty other message: %v", tb.messages)
		}
		if strings.Contains(joined, "SessionNotFound") {
			t.Fatalf("should not leak SessionNotFound:\n%s", joined)
		}

		ok := &fakeTB{}
		if !bargeTrace.Query().AllowEmpty().Check(ok).IsolatedFrom("ghost", WithOtherTrace(empty)) {
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

	t.Run("foreign_session_trace_calls_do_not_count_as_sent", func(t *testing.T) {
		own := isolationCall("a", "call_a", 2, "", `{
			"messages":[
				{"role":"user","content":"What is my biological age?"},
				{"role":"assistant","content":"42"},
				{"role":"user","content":"And what about my BMI?"}
			]
		}`)
		foreign := isolationCall("other", "call_foreign", 1, "", `{"messages":[{"role":"user","content":"What is my biological age?"}]}`)
		y := isolationCall("b", "call_b", 1, "", `{"messages":[{"role":"user","content":"What is my biological age?"}]}`)
		full := Trace{SessionID: "a", Calls: []Call{foreign, own}}
		tb := &fakeTB{}
		if full.Query().Since(Mark{Seq: 1}).Check(tb).IsolatedFrom("b", WithOtherTrace(Trace{SessionID: "b", Calls: []Call{y}})) {
			t.Fatal("foreign sessionTrace call must not mark Y's text as this session's own")
		}
	})

	t.Run("since_keeps_earlier_introduced_turns", func(t *testing.T) {
		first := isolationCall("a", "call_a1", 1, "", `{"messages":[{"role":"user","content":"What is my biological age?"}]}`)
		second := isolationCall("a", "call_a2", 2, "", `{
			"messages":[
				{"role":"user","content":"What is my biological age?"},
				{"role":"assistant","content":"42"},
				{"role":"user","content":"And what about my BMI?"}
			]
		}`)
		other := isolationCall("b", "call_b", 1, "", `{"messages":[{"role":"user","content":"What is my biological age?"}]}`)
		full := Trace{SessionID: "a", Calls: []Call{first, second}}
		tb := &fakeTB{}
		if !full.Query().Since(Mark{Seq: 1}).Check(tb).IsolatedFrom("b", WithOtherTrace(Trace{SessionID: "b", Calls: []Call{other}})) {
			t.Fatalf("earlier own turn must still count as sent: %v", tb.messages)
		}
	})

	t.Run("typo_other_id_fails", func(t *testing.T) {
		tb := &fakeTB{}
		if bargeTrace.Query().Check(tb).IsolatedFrom("barge-typo", WithOtherTrace(Trace{SessionID: "barge-typo"})) {
			t.Fatal("mistyped other session should fail")
		}
		joined := strings.Join(tb.messages, "\n")
		if !strings.Contains(joined, "no calls recorded for session barge-typo") ||
			!strings.Contains(joined, "isolated_from needs calls on both sides") {
			t.Fatalf("typo other message: %v", tb.messages)
		}
	})

	t.Run("later_call_introduces_all_new_user_messages", func(t *testing.T) {
		first := isolationCall("p", "call_p1", 1, "", `{"messages":[{"role":"user","content":"Opening question number one"}]}`)
		second := isolationCall("p", "call_p2", 2, "", `{
			"messages":[
				{"role":"user","content":"Opening question number one"},
				{"role":"user","content":"Second message sent by P itself"},
				{"role":"user","content":"Third message sent by P itself"}
			]
		}`)
		q := isolationCall("q", "call_q1", 1, "", `{"messages":[{"role":"user","content":"Second message sent by P itself"}]}`)
		tb := &fakeTB{}
		a := Trace{SessionID: "p", Calls: []Call{first, second}}.Query().Check(tb)
		if !a.IsolatedFrom("q", WithOtherTrace(Trace{SessionID: "q", Calls: []Call{q}})) {
			t.Fatalf("later-call new user messages are introduced: %v", tb.messages)
		}
	})

	t.Run("overlap_in_non_first_later_message", func(t *testing.T) {
		first := isolationCall("x", "call_x1", 1, "", `{"messages":[{"role":"user","content":"First turn from X itself"}]}`)
		second := isolationCall("x", "call_x2", 2, "", `{
			"messages":[
				{"role":"user","content":"First turn from X itself"},
				{"role":"assistant","content":"Noted."},
				{"role":"user","content":"Second turn from X itself"},
				{"role":"user","content":"Third turn from X itself"}
			]
		}`)
		y := isolationCall("y", "call_y1", 1, "", `{"messages":[{"role":"user","content":"Second turn from X itself"}]}`)
		tb := &fakeTB{}
		a := Trace{SessionID: "x", Calls: []Call{first, second}}.Query().Check(tb)
		if !a.IsolatedFrom("y", WithOtherTrace(Trace{SessionID: "y", Calls: []Call{y}})) {
			t.Fatalf("non-last later-call text is introduced by X: %v", tb.messages)
		}
	})

	t.Run("later_call_replayed_foreign_text", func(t *testing.T) {
		first := isolationCall("sess-x", "call_x1", 1, "", `{
			"messages":[
				{"role":"user","content":"Secret thread from the other app"},
				{"role":"assistant","content":"I need more data."},
				{"role":"user","content":"Continue from the saved thread."}
			]
		}`)
		second := isolationCall("sess-x", "call_x2", 2, "", `{
			"messages":[
				{"role":"user","content":"Secret thread from the other app"},
				{"role":"assistant","content":"I need more data."},
				{"role":"user","content":"Continue from the saved thread."},
				{"role":"user","content":"And one more question from X"}
			]
		}`)
		y := isolationCall("sess-y", "call_y1", 1, "", `{"messages":[{"role":"user","content":"Secret thread from the other app"}]}`)
		full := Trace{SessionID: "sess-x", Calls: []Call{first, second}}
		tb := &fakeTB{}
		if full.Query().Since(Mark{Seq: 1}).Check(tb).IsolatedFrom("sess-y", WithOtherTrace(Trace{SessionID: "sess-y", Calls: []Call{y}})) {
			t.Fatal("later-call window still sees replayed foreign history")
		}
		joined := strings.Join(tb.messages, "\n")
		if !strings.Contains(joined, "user text 'Secret thread from the other app'") ||
			!strings.Contains(joined, "appears in session sess-x #2 (call_x2)") {
			t.Fatalf("want call_x2 attribution:\n%s", joined)
		}
		if strings.Contains(joined, "#1 (call_x1)") {
			t.Fatalf("Since window must not attribute the leak to call_x1:\n%s", joined)
		}
	})

	t.Run("ignore_texts", func(t *testing.T) {
		origin := isolationCall("a", "call_a", 1, "", `{"messages":[{"role":"user","content":"What is my biological age?"}]}`)
		replay := isolationCall("b", "call_b", 1, "", `{
			"messages":[
				{"role":"user","content":"What is my biological age?"},
				{"role":"assistant","content":"42"},
				{"role":"user","content":"And what about my BMI?"}
			]
		}`)
		tb := &fakeTB{}
		a := Trace{SessionID: "b", Calls: []Call{replay}}.Query().Check(tb)
		if !a.IsolatedFrom("a", WithOtherTrace(Trace{SessionID: "a", Calls: []Call{origin}}), IgnoreTexts("What is my biological age?")) {
			t.Fatalf("IgnoreTexts should drop the replayed turn: %v", tb.messages)
		}
	})
}

func TestIntroducedUserTurns(t *testing.T) {
	first := isolationCall("p", "call_1", 1, "", `{
		"messages":[
			{"role":"user","content":"History from elsewhere"},
			{"role":"user","content":"Opening question number one"}
		]
	}`)
	later := isolationCall("p", "call_2", 2, "", `{
		"messages":[
			{"role":"user","content":"Opening question number one"},
			{"role":"user","content":"Second message sent by P itself"},
			{"role":"user","content":"Third message sent by P itself"}
		]
	}`)
	got := introducedUserTurns("p", []Call{later, first}, 8, nil)
	if _, ok := got[normalizeIsolationText("History from elsewhere")]; ok {
		t.Fatal("first-call inbound history must not be introduced")
	}
	if _, ok := got[normalizeIsolationText("Opening question number one")]; !ok {
		t.Fatal("first-call last user message must be introduced")
	}
	if _, ok := got[normalizeIsolationText("Second message sent by P itself")]; !ok {
		t.Fatal("later-call non-last new user message must be introduced")
	}
	if _, ok := got[normalizeIsolationText("Third message sent by P itself")]; !ok {
		t.Fatal("later-call last new user message must be introduced")
	}
}

func TestIsolationVectors(t *testing.T) {
	vectors, err := loadIsolationVectors(isolationVectorDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 8 {
		t.Fatalf("expected at least 8 isolation vectors, got %d", len(vectors))
	}
	for _, vec := range vectors {
		t.Run(vec.Name, func(t *testing.T) {
			runIsolationVector(t, vec)
		})
	}
}

func TestIsolationVectorsMatchPinnedProxy(t *testing.T) {
	if os.Getenv("LLMENTARY_LIVE_DOWNLOAD") != "1" && os.Getenv("LLMENTARY_CHECK_ISOLATION_VECTORS") != "1" {
		t.Skip("set LLMENTARY_LIVE_DOWNLOAD=1 or LLMENTARY_CHECK_ISOLATION_VECTORS=1 to compare testdata/isolation with the pinned proxy release")
	}
	local, err := loadIsolationVectorFiles(isolationVectorDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	remote, err := fetchProxyIsolationVectors(ctx)
	if err != nil {
		reportIsolationVectorFetch(t, err, os.Getenv("LLMENTARY_LIVE_DOWNLOAD") == "1")
	}
	if err := diffIsolationVectorFiles(local, remote); err != nil {
		t.Fatal(err)
	}
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

const isolationVectorDir = "testdata/isolation"

type isolationVector struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	AllowEmpty  bool                  `json:"allow_empty"`
	MinChars    *int                  `json:"min_chars"`
	IgnoreTexts []string              `json:"ignore_texts"`
	X           isolationVectorSide   `json:"x"`
	Y           isolationVectorSide   `json:"y"`
	Expect      isolationVectorExpect `json:"expect"`
}

type isolationVectorSide struct {
	ID    string                `json:"id"`
	Calls []isolationVectorCall `json:"calls"`
}

type isolationVectorExpect struct {
	Isolated        *bool    `json:"isolated"`
	MessageContains []string `json:"message_contains"`
}

type isolationVectorCall struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"session_id"`
	SessionSource string          `json:"session_source"`
	Seq           int             `json:"seq"`
	Request       json.RawMessage `json:"request"`
	RawRequest    json.RawMessage `json:"raw_request"`
}

func (c isolationVectorCall) toCall() (Call, error) {
	raw := c.Request
	if len(raw) == 0 {
		raw = c.RawRequest
	}
	body, err := isolationRawRequest(raw)
	if err != nil {
		return Call{}, err
	}
	return Call{
		ID:            c.ID,
		SessionID:     c.SessionID,
		SessionSource: c.SessionSource,
		Seq:           c.Seq,
		RawRequest:    body,
	}, nil
}

func isolationRawRequest(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if decoded, decErr := base64.StdEncoding.DecodeString(asString); decErr == nil && json.Valid(decoded) {
			return decoded, nil
		}
		return []byte(asString), nil
	}
	if json.Valid(raw) {
		return raw, nil
	}
	return nil, fmt.Errorf("isolation vector request is not JSON")
}

func loadIsolationVectors(dir string) ([]isolationVector, error) {
	files, err := loadIsolationVectorFiles(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]isolationVector, 0, len(names))
	for _, name := range names {
		var vec isolationVector
		if err := json.Unmarshal(files[name], &vec); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if vec.Name == "" {
			vec.Name = vec.ID
		}
		if vec.Name == "" {
			vec.Name = strings.TrimSuffix(name, ".json")
		}
		out = append(out, vec)
	}
	return out, nil
}

func loadIsolationVectorFiles(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") && !strings.EqualFold(entry.Name(), "README.md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out[entry.Name()] = data
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no isolation vectors in %s", dir)
	}
	return out, nil
}

func runIsolationVector(t *testing.T, vec isolationVector) {
	t.Helper()
	this, err := isolationVectorTrace(vec.X.ID, vec.X.Calls)
	if err != nil {
		t.Fatal(err)
	}
	other, err := isolationVectorTrace(vec.Y.ID, vec.Y.Calls)
	if err != nil {
		t.Fatal(err)
	}
	tb := &fakeTB{}
	q := this.Query()
	if vec.AllowEmpty {
		q = q.AllowEmpty()
	}
	var opts []IsolatedFromOption
	opts = append(opts, WithOtherTrace(other))
	if vec.MinChars != nil {
		opts = append(opts, MinChars(*vec.MinChars))
	}
	if len(vec.IgnoreTexts) > 0 {
		opts = append(opts, IgnoreTexts(vec.IgnoreTexts...))
	}
	ok := q.Check(tb).IsolatedFrom(vec.Y.ID, opts...)
	wantIsolated := vec.Expect.Isolated == nil || *vec.Expect.Isolated
	if wantIsolated {
		if !ok || tb.failed {
			t.Fatalf("want isolated, got fail: %v", tb.messages)
		}
		return
	}
	if ok {
		t.Fatal("want leak, got isolated")
	}
	joined := strings.Join(tb.messages, "\n")
	for _, want := range vec.Expect.MessageContains {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func isolationVectorTrace(sessionID string, calls []isolationVectorCall) (Trace, error) {
	out := make([]Call, 0, len(calls))
	for _, item := range calls {
		call, err := item.toCall()
		if err != nil {
			return Trace{}, err
		}
		out = append(out, call)
	}
	return Trace{SessionID: sessionID, Calls: out}, nil
}

// isolationVectorRef is the proxy git ref used to drift-check
// testdata/isolation. Default is the pinned release (ProxyVersion), not
// proxy main — main can move ahead of the pin (DOG-32/33 vectors).
// Override with LLMENTARY_ISOLATION_VECTORS_REF.
func isolationVectorRef() string {
	if env := strings.TrimSpace(os.Getenv("LLMENTARY_ISOLATION_VECTORS_REF")); env != "" {
		return env
	}
	return ProxyVersion
}

type isolationVectorHTTPError struct {
	Status int
	Ref    string
	URL    string
}

func (e isolationVectorHTTPError) Error() string {
	switch e.Status {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return fmt.Sprintf("GET %s: status=%d (ref %s): GitHub rate-limited or denied this public archive fetch; set GITHUB_TOKEN if you hit anonymous limits", e.URL, e.Status, e.Ref)
	case http.StatusNotFound:
		return fmt.Sprintf("GET %s: status=%d (ref %s): tag or ref not found", e.URL, e.Status, e.Ref)
	default:
		return fmt.Sprintf("GET %s: status=%d (ref %s)", e.URL, e.Status, e.Ref)
	}
}

func isolationVectorDeniedStatus(status int) bool {
	return status == http.StatusNotFound || status == http.StatusForbidden || status == http.StatusTooManyRequests
}

func isolationVectorDeniedDisposition(err error, liveDownload bool) (action, msg string, handled bool) {
	var httpErr isolationVectorHTTPError
	if !errors.As(err, &httpErr) || !isolationVectorDeniedStatus(httpErr.Status) {
		return "", "", false
	}
	msg = httpErr.Error()
	if liveDownload {
		return "fail", msg, true
	}
	return "skip", msg, true
}

func reportIsolationVectorFetch(t *testing.T, err error, liveDownload bool) {
	t.Helper()
	if action, msg, ok := isolationVectorDeniedDisposition(err, liveDownload); ok {
		if action == "fail" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	t.Fatal(err)
}

func httpStatusFromError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	const prefix = "status="
	msg := err.Error()
	i := strings.LastIndex(msg, prefix)
	if i < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(msg[i+len(prefix):])
	if j := strings.IndexAny(rest, " \t("); j >= 0 {
		rest = rest[:j]
	}
	n, convErr := strconv.Atoi(rest)
	if convErr != nil {
		return 0, false
	}
	return n, true
}

const defaultGitHubCodeloadRoot = "https://codeload.github.com"

func isolationArchiveURL(archiveRoot, repo, ref string) string {
	return strings.TrimRight(archiveRoot, "/") + "/" + repo + "/tar.gz/" + strings.TrimPrefix(ref, "/")
}

func fetchProxyIsolationVectors(ctx context.Context) (map[string][]byte, error) {
	token := optionalGitHubToken()
	ref := isolationVectorRef()
	return fetchGitHubIsolationDir(ctx, defaultGitHubCodeloadRoot, ref, token)
}

func fetchGitHubIsolationDir(ctx context.Context, archiveRoot, ref, token string) (map[string][]byte, error) {
	archiveURL := isolationArchiveURL(archiveRoot, defaultReleaseRepo, ref)
	body, err := httpGetBytesAccept(ctx, http.DefaultClient, archiveURL, token, maxArchiveBytes, "")
	if err != nil {
		if status, ok := httpStatusFromError(err); ok && isolationVectorDeniedStatus(status) {
			return nil, isolationVectorHTTPError{Status: status, Ref: ref, URL: archiveURL}
		}
		return nil, err
	}
	out, err := isolationFilesFromTarGz(body)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("proxy testdata/isolation is empty at ref %s", ref)
	}
	return out, nil
}

func isolationTarRel(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	const marker = "testdata/isolation/"
	i := strings.Index(name, marker)
	if i < 0 {
		return "", false
	}
	rel := name[i+len(marker):]
	if rel == "" || strings.Contains(rel, "/") {
		return "", false
	}
	return rel, true
}

func isolationFilesFromTarGz(raw []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("isolation archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("isolation archive: %w", err)
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		rel, ok := isolationTarRel(hdr.Name)
		if !ok || strings.HasPrefix(rel, ".") {
			continue
		}
		if !strings.HasSuffix(rel, ".json") && !strings.EqualFold(rel, "README.md") {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, 1<<20+1))
		if err != nil {
			return nil, fmt.Errorf("isolation archive %s: %w", rel, err)
		}
		if len(data) > 1<<20 {
			return nil, fmt.Errorf("isolation archive %s: file exceeds 1MiB", rel)
		}
		out[path.Base(rel)] = data
	}
	return out, nil
}

func diffIsolationVectorFiles(local, remote map[string][]byte) error {
	var missing, extra, changed []string
	for name := range remote {
		if _, ok := local[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name, localBody := range local {
		remoteBody, ok := remote[name]
		if !ok {
			extra = append(extra, name)
			continue
		}
		if !bytesEqualJSON(localBody, remoteBody) {
			changed = append(changed, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(changed)
	if len(missing) == 0 && len(extra) == 0 && len(changed) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "vendored testdata/isolation drifted from the proxy copy (missing=%v extra=%v changed=%v)\n", missing, extra, changed)
	names := make([]string, 0, len(remote))
	for name := range remote {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "\n--- proxy testdata/isolation/%s ---\n%s", name, remote[name])
		if !strings.HasSuffix(string(remote[name]), "\n") {
			b.WriteByte('\n')
		}
	}
	return fmt.Errorf("%s", b.String())
}

func bytesEqualJSON(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil {
		return jsonDeepEqual(left, right)
	}
	return string(a) == string(b)
}

func TestIsolationArchiveURLUsesPublicCodeload(t *testing.T) {
	if defaultGitHubCodeloadRoot != "https://codeload.github.com" {
		t.Fatalf("codeload root=%q", defaultGitHubCodeloadRoot)
	}
	got := isolationArchiveURL(defaultGitHubCodeloadRoot, defaultReleaseRepo, ProxyVersion)
	want := "https://codeload.github.com/" + defaultReleaseRepo + "/tar.gz/" + ProxyVersion
	if got != want {
		t.Fatalf("archive URL=%q want %q", got, want)
	}
	if strings.Contains(got, "api.github.com") || strings.Contains(got, "/contents/") {
		t.Fatalf("must not use the GitHub REST API: %s", got)
	}
}

func TestIsolationVectorRefDefaultsToPinnedRelease(t *testing.T) {
	t.Setenv("LLMENTARY_ISOLATION_VECTORS_REF", "")
	if got := isolationVectorRef(); got != ProxyVersion {
		t.Fatalf("isolationVectorRef()=%q want pinned %q", got, ProxyVersion)
	}
	if isolationVectorRef() == "main" {
		t.Fatal("isolation vector drift must not follow proxy main")
	}
	t.Setenv("LLMENTARY_ISOLATION_VECTORS_REF", "main")
	if got := isolationVectorRef(); got != "main" {
		t.Fatalf("override isolationVectorRef()=%q", got)
	}
}

func TestIsolationVectorFetchDeniedNeverPasses(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusTooManyRequests} {
		err := isolationVectorHTTPError{Status: status, Ref: "main", URL: "https://example.test/isolation"}
		action, msg, ok := isolationVectorDeniedDisposition(err, true)
		if !ok || action != "fail" || !strings.Contains(msg, fmt.Sprintf("status=%d", status)) {
			t.Fatalf("live HTTP %d: action=%q msg=%q ok=%v", status, action, msg, ok)
		}
		if status != http.StatusNotFound && !strings.Contains(msg, "rate-limited") {
			t.Fatalf("HTTP %d error should mention rate-limited: %s", status, msg)
		}
		if status == http.StatusNotFound && !strings.Contains(msg, "tag or ref not found") {
			t.Fatalf("HTTP 404 error should name the missing tag: %s", msg)
		}
		action, msg, ok = isolationVectorDeniedDisposition(err, false)
		if !ok || action != "skip" || !strings.Contains(msg, fmt.Sprintf("status=%d", status)) {
			t.Fatalf("offline HTTP %d: action=%q msg=%q ok=%v", status, action, msg, ok)
		}
	}
	if _, _, ok := isolationVectorDeniedDisposition(errors.New("GET x: status=500"), true); ok {
		t.Fatal("500 must not be treated as a denied compare")
	}
}

func TestFetchGitHubIsolationDirDeniedIsError(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusTooManyRequests} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		_, err := fetchGitHubIsolationDir(context.Background(), srv.URL, "missing-tag", "")
		srv.Close()
		var httpErr isolationVectorHTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != status || httpErr.Ref != "missing-tag" {
			t.Fatalf("status %d: err=%v", status, err)
		}
		if !strings.Contains(err.Error(), "/tar.gz/missing-tag") {
			t.Fatalf("status %d URL should be the public archive: %v", status, err)
		}
		if action, _, ok := isolationVectorDeniedDisposition(err, true); !ok || action != "fail" {
			t.Fatalf("status %d must fail the live drift check, action=%q ok=%v", status, action, ok)
		}
	}
}

func TestFetchGitHubIsolationDirQueriesPinnedRef(t *testing.T) {
	t.Setenv("LLMENTARY_ISOLATION_VECTORS_REF", "")
	empty := isolationTestTarGz(t, nil)
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if strings.Contains(r.URL.RawQuery, "ref=") || strings.Contains(r.URL.Path, "/contents/") {
			http.Error(w, "contents API is not allowed", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(empty)
	}))
	t.Cleanup(srv.Close)
	_, err := fetchGitHubIsolationDir(context.Background(), srv.URL, isolationVectorRef(), "")
	if err == nil || !strings.Contains(err.Error(), ProxyVersion) {
		t.Fatalf("empty pin listing err=%v", err)
	}
	wantPath := "/" + defaultReleaseRepo + "/tar.gz/" + ProxyVersion
	if gotPath != wantPath {
		t.Fatalf("archive path=%q want %q", gotPath, wantPath)
	}
	if strings.Contains(gotPath, "main") {
		t.Fatal("must not query proxy main for the default drift check")
	}
}

func TestFetchGitHubIsolationDirExtractsFiles(t *testing.T) {
	payload := []byte(`{"id":"vec","name":"from-archive"}`)
	archive := isolationTestTarGz(t, map[string][]byte{
		"README.md":        []byte("# isolation"),
		"vector.json":      payload,
		"nested/skip.json": []byte(`{}`),
		"notes.txt":        []byte("ignore"),
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	got, err := fetchGitHubIsolationDir(context.Background(), srv.URL, ProxyVersion, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(got["vector.json"]) != string(payload) || string(got["README.md"]) != "# isolation" {
		t.Fatalf("extracted %#v", got)
	}
	if _, ok := got["skip.json"]; ok {
		t.Fatal("nested isolation files must be ignored")
	}
	if _, ok := got["notes.txt"]; ok {
		t.Fatal("non-json extras must be ignored")
	}
}

func TestFetchProxyIsolationVectorsUsesOptionalTokenOnly(t *testing.T) {
	clearGitHubTokens(t)
	t.Setenv("LLMTESTLAB_REPO_TOKEN", "stale-pat")
	if got := optionalGitHubToken(); got != "" {
		t.Fatalf("stale PAT must not feed isolation fetch: %q", got)
	}

	empty := isolationTestTarGz(t, nil)
	var sawAuth, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		_, _ = w.Write(empty)
	}))
	t.Cleanup(srv.Close)
	_, err := fetchGitHubIsolationDir(context.Background(), srv.URL, ProxyVersion, optionalGitHubToken())
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty listing err=%v", err)
	}
	if sawAuth != "" {
		t.Fatalf("anonymous isolation fetch sent Authorization=%q", sawAuth)
	}
	if !strings.Contains(sawPath, "/tar.gz/"+ProxyVersion) {
		t.Fatalf("anonymous fetch path=%q", sawPath)
	}

	t.Setenv("GITHUB_TOKEN", "rate-limit-token")
	sawAuth = ""
	_, err = fetchGitHubIsolationDir(context.Background(), srv.URL, ProxyVersion, optionalGitHubToken())
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty listing with optional token err=%v", err)
	}
	if sawAuth != "Bearer rate-limit-token" {
		t.Fatalf("optional token Authorization=%q", sawAuth)
	}
}

func isolationTestTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	prefix := "llmentary-0.4.1/testdata/isolation/"
	if err := tw.WriteHeader(&tar.Header{Name: prefix, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		hdr := &tar.Header{Name: prefix + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
