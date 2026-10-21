// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	t.Run("empty_other_fails_unless_allow_empty", func(t *testing.T) {
		empty := Trace{SessionID: "ghost"}
		tb := &fakeTB{}
		if bargeTrace.Query().Check(tb).IsolatedFrom("ghost", WithOtherTrace(empty)) {
			t.Fatal("empty other session should fail")
		}
		joined := strings.Join(tb.messages, "\n")
		if !strings.Contains(joined, "no calls recorded for session ghost") {
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

// isolationVectorsProxyRef is the proxy git ref that publishes
// testdata/isolation (DOG-29 / barzoj-co/llmentary#35, merged as bb39133).
// Override with LLMENTARY_ISOLATION_VECTORS_REF.
const isolationVectorsProxyRef = "main"

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

func isolationVectorRef() string {
	if env := strings.TrimSpace(os.Getenv("LLMENTARY_ISOLATION_VECTORS_REF")); env != "" {
		return env
	}
	return isolationVectorsProxyRef
}

type isolationVectorHTTPError struct {
	Status int
	Ref    string
	URL    string
}

func (e isolationVectorHTTPError) Error() string {
	return fmt.Sprintf("GET %s: status=%d (ref %s)", e.URL, e.Status, e.Ref)
}

func isolationVectorDeniedDisposition(err error, liveDownload bool) (action, msg string, handled bool) {
	var httpErr isolationVectorHTTPError
	if !errors.As(err, &httpErr) || (httpErr.Status != http.StatusNotFound && httpErr.Status != http.StatusForbidden) {
		return "", "", false
	}
	msg = fmt.Sprintf("proxy testdata/isolation: HTTP %d at ref %s — cannot compare vendored copy", httpErr.Status, httpErr.Ref)
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

func fetchProxyIsolationVectors(ctx context.Context) (map[string][]byte, error) {
	token := firstNonEmpty(os.Getenv("LLMENTARY_GITHUB_TOKEN"), os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"), os.Getenv("LLMTESTLAB_REPO_TOKEN"))
	ref := isolationVectorRef()
	return fetchGitHubIsolationDir(ctx, strings.TrimRight(defaultGitHubAPIRoot, "/"), ref, token)
}

func fetchGitHubIsolationDir(ctx context.Context, apiRoot, ref, token string) (map[string][]byte, error) {
	listURL := strings.TrimRight(apiRoot, "/") + "/repos/" + defaultReleaseRepo + "/contents/testdata/isolation?ref=" + url.QueryEscape(ref)
	body, err := httpGetBytesAccept(ctx, http.DefaultClient, listURL, token, 1<<20, "application/vnd.github+json")
	if err != nil {
		if status, ok := httpStatusFromError(err); ok && (status == http.StatusNotFound || status == http.StatusForbidden) {
			return nil, isolationVectorHTTPError{Status: status, Ref: ref, URL: listURL}
		}
		return nil, err
	}
	var entries []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse isolation dir at %s: %w", ref, err)
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		if entry.Type != "file" || strings.HasPrefix(entry.Name, ".") {
			continue
		}
		if !strings.HasSuffix(entry.Name, ".json") && !strings.EqualFold(entry.Name, "README.md") {
			continue
		}
		if entry.DownloadURL == "" {
			return nil, fmt.Errorf("isolation file %s at %s has no download_url", entry.Name, ref)
		}
		data, err := httpGetBytesAccept(ctx, http.DefaultClient, entry.DownloadURL, token, 1<<20, "application/vnd.github.raw")
		if err != nil {
			if status, ok := httpStatusFromError(err); ok && (status == http.StatusNotFound || status == http.StatusForbidden) {
				return nil, isolationVectorHTTPError{Status: status, Ref: ref, URL: entry.DownloadURL}
			}
			return nil, fmt.Errorf("download %s: %w", entry.Name, err)
		}
		out[path.Base(entry.Name)] = data
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("proxy testdata/isolation is empty at ref %s", ref)
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

func TestIsolationVectorRefIsProxyMain(t *testing.T) {
	t.Setenv("LLMENTARY_ISOLATION_VECTORS_REF", "")
	if isolationVectorsProxyRef != "main" {
		t.Fatalf("isolationVectorsProxyRef=%q want main", isolationVectorsProxyRef)
	}
	if got := isolationVectorRef(); got != "main" {
		t.Fatalf("isolationVectorRef()=%q want main", got)
	}
	t.Setenv("LLMENTARY_ISOLATION_VECTORS_REF", "feature-branch")
	if got := isolationVectorRef(); got != "feature-branch" {
		t.Fatalf("override isolationVectorRef()=%q", got)
	}
}

func TestIsolationVectorFetchDeniedNeverPasses(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		err := isolationVectorHTTPError{Status: status, Ref: "main", URL: "https://example.test/isolation"}
		action, msg, ok := isolationVectorDeniedDisposition(err, true)
		if !ok || action != "fail" || !strings.Contains(msg, fmt.Sprintf("HTTP %d", status)) {
			t.Fatalf("live HTTP %d: action=%q msg=%q ok=%v", status, action, msg, ok)
		}
		action, msg, ok = isolationVectorDeniedDisposition(err, false)
		if !ok || action != "skip" || !strings.Contains(msg, fmt.Sprintf("HTTP %d", status)) {
			t.Fatalf("offline HTTP %d: action=%q msg=%q ok=%v", status, action, msg, ok)
		}
	}
	if _, _, ok := isolationVectorDeniedDisposition(errors.New("GET x: status=500"), true); ok {
		t.Fatal("500 must not be treated as a denied compare")
	}
}

func TestFetchGitHubIsolationDirDeniedIsError(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		_, err := fetchGitHubIsolationDir(context.Background(), srv.URL, "main", "")
		srv.Close()
		var httpErr isolationVectorHTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != status || httpErr.Ref != "main" {
			t.Fatalf("status %d: err=%v", status, err)
		}
		if action, _, ok := isolationVectorDeniedDisposition(err, true); !ok || action != "fail" {
			t.Fatalf("status %d must fail the live drift check, action=%q ok=%v", status, action, ok)
		}
	}
}
