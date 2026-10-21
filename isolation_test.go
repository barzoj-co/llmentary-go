// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
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
			"user text",
			"What is my biological age?",
			"#1 (call_a)",
			"#1 (call_b)",
			"a",
			"b",
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
		t.Skip("set LLMENTARY_LIVE_DOWNLOAD=1 or LLMENTARY_CHECK_ISOLATION_VECTORS=1 to compare testdata/isolation with the proxy")
	}
	local, err := loadIsolationVectorFiles(isolationVectorDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	remote, err := fetchProxyIsolationVectors(ctx, ProxyVersion)
	if err != nil {
		t.Fatal(err)
	}
	if remote == nil {
		t.Logf("proxy %s has no testdata/isolation yet; using DOG-29 text until the proxy docs PR lands", ProxyVersion)
		return
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
	Name            string                `json:"name"`
	Expect          string                `json:"expect"`
	ThisSession     string                `json:"this_session"`
	OtherSession    string                `json:"other_session"`
	AllowEmpty      bool                  `json:"allow_empty"`
	MinChars        *int                  `json:"min_chars"`
	IgnoreTexts     []string              `json:"ignore_texts"`
	MessageContains []string              `json:"message_contains"`
	This            []isolationVectorCall `json:"this"`
	Other           []isolationVectorCall `json:"other"`
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
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
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
	this, err := isolationVectorTrace(vec.ThisSession, vec.This)
	if err != nil {
		t.Fatal(err)
	}
	other, err := isolationVectorTrace(vec.OtherSession, vec.Other)
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
	ok := q.Check(tb).IsolatedFrom(vec.OtherSession, opts...)
	switch strings.ToLower(vec.Expect) {
	case "pass":
		if !ok || tb.failed {
			t.Fatalf("want pass, got fail: %v", tb.messages)
		}
	case "fail":
		if ok {
			t.Fatal("want fail, got pass")
		}
		joined := strings.Join(tb.messages, "\n")
		for _, want := range vec.MessageContains {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q in:\n%s", want, joined)
			}
		}
	default:
		t.Fatalf("unknown expect %q", vec.Expect)
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

func fetchProxyIsolationVectors(ctx context.Context, version string) (map[string][]byte, error) {
	token := firstNonEmpty(os.Getenv("LLMENTARY_GITHUB_TOKEN"), os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"), os.Getenv("LLMTESTLAB_REPO_TOKEN"))
	tag, _ := normalizeReleaseVersion(version)
	for _, ref := range []string{tag, "main"} {
		files, missing, err := fetchGitHubIsolationDir(ctx, ref, token)
		if err != nil {
			return nil, err
		}
		if missing {
			continue
		}
		return files, nil
	}
	return nil, nil
}

func fetchGitHubIsolationDir(ctx context.Context, ref, token string) (map[string][]byte, bool, error) {
	apiRoot := strings.TrimRight(defaultGitHubAPIRoot, "/")
	listURL := apiRoot + "/repos/" + defaultReleaseRepo + "/contents/testdata/isolation?ref=" + ref
	body, err := httpGetBytesAccept(ctx, http.DefaultClient, listURL, token, 1<<20, "application/vnd.github+json")
	if err != nil {
		if strings.Contains(err.Error(), "status=404") {
			return nil, true, nil
		}
		return nil, false, err
	}
	var entries []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, false, fmt.Errorf("parse isolation dir at %s: %w", ref, err)
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		if entry.Type != "file" || !strings.HasSuffix(entry.Name, ".json") {
			continue
		}
		if entry.DownloadURL == "" {
			return nil, false, fmt.Errorf("isolation file %s at %s has no download_url", entry.Name, ref)
		}
		data, err := httpGetBytesAccept(ctx, http.DefaultClient, entry.DownloadURL, token, 1<<20, "application/vnd.github.raw")
		if err != nil {
			return nil, false, fmt.Errorf("download %s: %w", entry.Name, err)
		}
		out[path.Base(entry.Name)] = data
	}
	if len(out) == 0 {
		return nil, true, nil
	}
	return out, false, nil
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
	if len(missing) == 0 && len(extra) == 0 && len(changed) == 0 {
		return nil
	}
	return fmt.Errorf("vendored testdata/isolation drifted from the proxy copy (missing=%v extra=%v changed=%v)", missing, extra, changed)
}

func bytesEqualJSON(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil {
		return jsonDeepEqual(left, right)
	}
	return string(a) == string(b)
}
