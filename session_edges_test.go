// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionControlAndMark(t *testing.T) {
	var deleted string
	var sawSessionHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/sessions/"):
			deleted = strings.TrimPrefix(r.URL.Path, "/sessions/")
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessions": []SessionInfo{{ID: "known", Calls: 1}},
			})
		case r.URL.Path == "/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "0.4.0-test"})
		case strings.HasSuffix(r.URL.Path, "/wait") || strings.Count(r.URL.Path, "/") == 2:
			_ = json.NewEncoder(w).Encode(sessionPayload{
				ID: "sess",
				Calls: []Call{{
					ID:  "c1",
					Seq: 4,
					Normalized: Normalized{
						Error: &CallError{Type: "api", Message: "nope", MidStream: true},
						Usage: &Usage{TotalTokens: intPtrForTest(7)},
					},
				}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	lm := New(t,
		WithControlURL(srv.URL),
		WithProxyURL("http://127.0.0.1:9"),
		WithWait(time.Millisecond, time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	list := lm.ListSessions(ctx, Source("header"))
	if len(list) != 1 || list[0].ID != "known" {
		t.Fatalf("ListSessions=%+v", list)
	}
	if got := lm.Version(ctx); got != "0.4.0-test" {
		t.Fatalf("Version=%q", got)
	}
	tr := lm.Calls(ctx)
	if tr.SessionID != lm.SessionID() || len(tr.Calls) != 1 || tr.Calls[0].Seq != 4 {
		t.Fatalf("Calls=%+v", tr)
	}
	mark := lm.Mark(ctx)
	if mark.Seq != 4 {
		t.Fatalf("Mark=%+v", mark)
	}

	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSessionHeader = r.Header.Get(SessionHeaderName)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(probe.Close)
	req, err := http.NewRequest(http.MethodGet, probe.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := lm.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if sawSessionHeader != lm.SessionID() {
		t.Fatalf("HTTPClient header=%q want %q", sawSessionHeader, lm.SessionID())
	}
	if err := lm.client.Delete(ctx, lm.SessionID()); err != nil {
		t.Fatal(err)
	}
	if deleted != lm.SessionID() {
		t.Fatalf("Delete id=%q want %q", deleted, lm.SessionID())
	}
}

func TestSessionMarkEmptyAndControlErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"session_not_found","message":"missing"}}`)
	}))
	t.Cleanup(srv.Close)
	lm := New(t, WithControlURL(srv.URL), WithProxyURL("http://127.0.0.1:9"))
	mark := lm.Mark(context.Background())
	if mark.Seq != 0 {
		t.Fatalf("missing session Mark=%+v", mark)
	}

	tb := &fakeTB{}
	failing := &Session{t: tb, client: lm.client, sessionID: "x"}
	_ = failing.ListSessions(context.Background())
	_ = failing.Version(context.Background())
	_ = failing.Calls(context.Background())
	if !tb.fatal {
		t.Fatal("control errors should Fatalf")
	}
}

func TestAssertionErrorAndTokenEdges(t *testing.T) {
	retry := 1
	trace := Trace{SessionID: "s", Calls: []Call{
		{
			Seq:        1,
			RetryCount: &retry,
			Normalized: Normalized{
				Error: &CallError{Type: "rate", Message: "slow", MidStream: true},
				Usage: &Usage{TotalTokens: intPtrForTest(10)},
			},
		},
		{Seq: 2, Normalized: Normalized{Usage: &Usage{InputTokens: intPtrForTest(1), OutputTokens: intPtrForTest(1)}}},
	}}
	a := &Assertions{t: t, trace: trace, fatal: true}
	if !a.HasError() {
		t.Fatal("HasError")
	}
	if !a.MidStreamError() {
		t.Fatal("MidStreamError")
	}
	if !a.TokensAtMost(12) {
		t.Fatal("TokensAtMost inclusive bound")
	}
	q := trace.Query().Errors()
	if q.Len() != 1 || q.Trace().SessionID != "s" || q.Trace().Calls[0].Seq != 1 {
		t.Fatalf("Errors query=%+v", q.Trace())
	}
	if trace.Query().Retries().Len() != 1 {
		t.Fatalf("Retries=%d", trace.Query().Retries().Len())
	}
	if a.Trace().SessionID != "s" {
		t.Fatalf("Assertions.Trace=%+v", a.Trace())
	}

	tb := &fakeTB{}
	clean := &Assertions{t: tb, trace: Trace{SessionID: "s", Calls: []Call{{Seq: 1}}}}
	if clean.HasError() || clean.MidStreamError() {
		t.Fatal("clean trace should fail HasError/MidStreamError")
	}
	if !tb.failed {
		t.Fatal("expected failures")
	}
	if !strings.Contains(strings.Join(tb.messages, "\n"), "expected at least one LLM error") {
		t.Fatalf("messages=%v", tb.messages)
	}

	tb = &fakeTB{}
	over := &Assertions{t: tb, trace: trace}
	if over.TokensAtMost(11) {
		t.Fatal("12 tokens should exceed 11")
	}
	if !strings.Contains(strings.Join(tb.messages, "\n"), "want <=11") {
		t.Fatalf("TokensAtMost message=%v", tb.messages)
	}
}

func TestSpecPackageAliases(t *testing.T) {
	clean := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "support-bot-run1-clean.json")}}
	AssertNoSpecViolations(t, clean)
	AssertNoIgnoredFields(t, clean)
	ignored := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "ignored-fields.json")}}
	AssertNoIgnoredFields(t, ignored, IgnorePaths("enumDescriptions"))
}

func TestTraceIgnoredFieldsAndJSONHelpers(t *testing.T) {
	trace := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "ignored-fields.json")}}
	fields := trace.IgnoredFields()
	if len(fields) == 0 {
		t.Fatal("IgnoredFields should concatenate recorded entries")
	}
	found := false
	for _, field := range fields {
		if strings.Contains(field.Kind, "enumDescriptions") || strings.Contains(field.Path, "enumDescriptions") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("IgnoredFields=%+v", fields)
	}

	if jsonSubset("x", map[string]any{"a": 1}) {
		t.Fatal("scalar is not an object subset")
	}
	if jsonSubset([]any{"a"}, []any{"a", "b"}) {
		t.Fatal("shorter array is not a subset")
	}
	if !jsonSubset([]any{"a", "b"}, []any{"a"}) {
		t.Fatal("array prefix should be a subset")
	}
	value := map[string]any{"items": []any{map[string]any{"n": float64(1)}}}
	got, ok := jsonPath(value, "$.items.0.n")
	if !ok || got != float64(1) {
		t.Fatalf("jsonPath=%v ok=%v", got, ok)
	}
	if _, ok := jsonPath("scalar", "n"); ok {
		t.Fatal("jsonPath through scalar")
	}
	if keys := requiredKeys([]string{"a", "b"}); len(keys) != 2 || keys[0] != "a" {
		t.Fatalf("requiredKeys strings=%v", keys)
	}
	if keys := requiredKeys([]any{"a", 1, "b"}); len(keys) != 2 || keys[1] != "b" {
		t.Fatalf("requiredKeys mixed=%v", keys)
	}
	if requiredKeys(3) != nil {
		t.Fatal("requiredKeys default")
	}
	if !matchesJSONType([]any{}, "array") || !matchesJSONType(true, "boolean") || !matchesJSONType(nil, "null") || !matchesJSONType(1.5, "number") {
		t.Fatal("matchesJSONType known kinds")
	}
	if matchesJSONType("x", "number") || !matchesJSONType("x", "other") {
		t.Fatal("matchesJSONType mismatches")
	}
	if compactJSON("not-json") != "not-json" || compactJSON("") != "{}" {
		t.Fatal("compactJSON fallback")
	}
}

func TestArgsSubsetAndResponseMatchesFailures(t *testing.T) {
	trace := fixtureTrace()
	tb := &fakeTB{}
	a := &Assertions{t: tb, trace: trace}
	if a.ToolCalled("get_weather", ArgsSubset(map[string]any{"city": "Berlin"})) {
		t.Fatal("wrong city should fail ArgsSubset")
	}
	if a.ResponseMatches(`(`) {
		t.Fatal("invalid regexp should fail")
	}
	if !tb.failed {
		t.Fatal("expected assertion failures")
	}
}

func TestQueryIndexOutOfRangeAndGetenvDefault(t *testing.T) {
	q := fixtureTrace().Query().Index(99)
	if q.Len() != 0 {
		t.Fatalf("out-of-range Index should drop calls, got %d", q.Len())
	}
	t.Setenv("LLMENTARY_TEST_DEFAULT", "")
	if got := getenvDefault("LLMENTARY_TEST_DEFAULT", "fallback"); got != "fallback" {
		t.Fatalf("empty env=%q", got)
	}
	t.Setenv("LLMENTARY_TEST_DEFAULT", "set")
	if got := getenvDefault("LLMENTARY_TEST_DEFAULT", "fallback"); got != "set" {
		t.Fatalf("set env=%q", got)
	}
}

func TestClientVersionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Version(context.Background()); err == nil {
		t.Fatal("expected Version error")
	}
}

func TestIsNumericToken(t *testing.T) {
	if isNumericToken("") || isNumericToken("20C") || isNumericToken(".") || isNumericToken("x1") {
		t.Fatal("non-numeric tokens")
	}
	if !isNumericToken("20") || !isNumericToken("20.5") {
		t.Fatal("numeric tokens")
	}
}
