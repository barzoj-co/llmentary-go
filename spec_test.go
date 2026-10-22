// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadSpecCall(t *testing.T, name string) Call {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "spec", name))
	if err != nil {
		t.Fatal(err)
	}
	var call Call
	if err := json.Unmarshal(data, &call); err != nil {
		t.Fatal(err)
	}
	return call
}

func TestSpecAccessorsGoldenPayloads(t *testing.T) {
	clean := loadSpecCall(t, "chat-service-run1-clean.json")
	if clean.Validation() == nil {
		t.Fatal("chat-service run-1 fixture must expose validation metadata")
	}
	if clean.Validation().Mode != "warn" || clean.Validation().Profile != "relaxed" {
		t.Fatalf("validation=%+v", clean.Validation())
	}
	findings, checked := clean.SpecViolations()
	if !checked {
		t.Fatal("spec_violations:[] must count as checked")
	}
	if len(findings) != 0 {
		t.Fatalf("chat-service run-1 must read as clean, got %#v", findings)
	}

	absent := loadSpecCall(t, "not-checked.json")
	if absent.Validation() != nil {
		t.Fatal("not-checked fixture should have no validation block")
	}
	if _, ok := absent.SpecViolations(); ok {
		t.Fatal("absent spec_violations must be not checked")
	}

	violations := loadSpecCall(t, "violations.json")
	got, ok := violations.SpecViolations()
	if !ok || len(got) != 2 {
		t.Fatalf("violations checked=%v n=%d", ok, len(got))
	}
	if got[0].Pointer != "/foo" || got[0].Keyword != "unevaluatedProperties" {
		t.Fatalf("first finding=%+v", got[0])
	}

	ignored := loadSpecCall(t, "ignored-fields.json")
	fields, ok := ignored.IgnoredFields()
	if !ok || len(fields) != 1 || fields[0].Kind != "enumDescriptions" {
		t.Fatalf("ignored=%#v checked=%v", fields, ok)
	}
}

func TestNoSpecViolationsContract(t *testing.T) {
	clean := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "chat-service-run1-clean.json")}}
	if !(&Assertions{t: t, trace: clean, fatal: true}).NoSpecViolations() {
		t.Fatal("clean chat-service payload should pass")
	}

	violations := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "violations.json")}}
	tb := &fakeTB{}
	(&Assertions{t: tb, trace: violations}).NoSpecViolations()
	joined := strings.Join(tb.messages, "\n")
	for _, want := range []string{
		"#1 openai.chat request foo: additional properties not allowed — unevaluatedProperties",
		"validation was not run",
	} {
		if want == "validation was not run" {
			if strings.Contains(joined, want) {
				t.Fatalf("unexpected not-run on a checked fixture:\n%s", joined)
			}
			continue
		}
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}

	off := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "not-checked.json")}}
	offTB := &fakeTB{}
	(&Assertions{t: offTB, trace: off}).NoSpecViolations()
	if !strings.Contains(strings.Join(offTB.messages, "\n"), "validation was not run") {
		t.Fatalf("off mode message=%q", offTB.messages)
	}
	if !(&Assertions{t: t, trace: off, fatal: true}).NoSpecViolations(AllowUnvalidated()) {
		t.Fatal("allow_unvalidated should pass when validation was not run")
	}
}

func TestNoSpecViolationsOptions(t *testing.T) {
	trace := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "violations.json")}}

	if !(&Assertions{t: t, trace: trace, fatal: true}).NoSpecViolations(Side("request"), IgnorePaths("foo", "/foo")) {
		t.Fatal("ignore_paths should drop the request finding")
	}
	if !(&Assertions{t: t, trace: trace, fatal: true}).NoSpecViolations(IgnoreKeywords("unevaluatedProperties")) {
		t.Fatal("ignore_keywords should drop both findings")
	}

	sideTB := &fakeTB{}
	(&Assertions{t: sideTB, trace: trace}).NoSpecViolations(Side("response"))
	sideMsg := strings.Join(sideTB.messages, "\n")
	if !strings.Contains(sideMsg, "response") || strings.Contains(sideMsg, " request ") {
		t.Fatalf("side filter message=%q", sideMsg)
	}

	callsTB := &fakeTB{}
	(&Assertions{t: callsTB, trace: trace}).NoSpecViolations(Calls(99))
	callsMsg := strings.Join(callsTB.messages, "\n")
	if !strings.Contains(callsMsg, "validation was not run") || !strings.Contains(callsMsg, "99") {
		t.Fatalf("empty Calls filter must not be vacuous: %q", callsTB.messages)
	}
}

func TestPathMatchesWholeSegments(t *testing.T) {
	t.Parallel()
	if !pathMatches("foo", "/foo", "foo") {
		t.Fatal("exact path should match")
	}
	if !pathMatches("tools.0.function.parameters.properties.unit.enumDescriptions", "", "enumDescriptions") {
		t.Fatal("whole final segment should match")
	}
	if pathMatches("foobar", "", "foo") {
		t.Fatal("IgnorePaths(foo) must not hide foobar")
	}
	if pathMatches("tools.0.name", "", "tool") {
		t.Fatal("IgnorePaths(tool) must not hide tools.*")
	}
	if pathMatches("tools.0.function.valid", "", "id") {
		t.Fatal("IgnorePaths(id) must not hide a path ending in valid")
	}
}

func TestNoIgnoredFieldsContract(t *testing.T) {
	clean := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "chat-service-run1-clean.json")}}
	ignored := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "ignored-fields.json")}}
	off := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "not-checked.json")}}

	if !(&Assertions{t: t, trace: clean, fatal: true}).NoIgnoredFields() {
		t.Fatal("clean chat-service payload should pass NoIgnoredFields")
	}
	tb := &fakeTB{}
	(&Assertions{t: tb, trace: ignored}).NoIgnoredFields()
	if !strings.Contains(strings.Join(tb.messages, "\n"), "enumDescriptions") {
		t.Fatalf("ignored fields message=%q", tb.messages)
	}
	if !(&Assertions{t: t, trace: ignored, fatal: true}).NoIgnoredFields(IgnorePaths("enumDescriptions")) {
		t.Fatal("ignore_paths should drop enumDescriptions")
	}
	offTB := &fakeTB{}
	(&Assertions{t: offTB, trace: off}).NoIgnoredFields()
	if !strings.Contains(strings.Join(offTB.messages, "\n"), "validation was not run") {
		t.Fatalf("off ignored-fields message=%q", offTB.messages)
	}
}

func TestTraceSpecViolationsAndSummary(t *testing.T) {
	trace := Trace{SessionID: "s", Calls: []Call{
		loadSpecCall(t, "chat-service-run1-clean.json"),
		loadSpecCall(t, "violations.json"),
		loadSpecCall(t, "not-checked.json"),
	}}
	trace.Calls[1].Seq = 2
	trace.Calls[2].Seq = 3
	if n := len(trace.SpecViolations()); n != 2 {
		t.Fatalf("trace findings=%d", n)
	}
	summary := trace.Summary()
	for _, want := range []string{"spec=clean", "spec=2 violations", "spec=not checked"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestSpecStatusLabelRequestOnly(t *testing.T) {
	requestOnly := Call{Normalized: Normalized{
		Validation:     &Validation{RequestChecked: true, ResponseChecked: false},
		SpecViolations: &[]SpecViolation{},
	}}
	if got := requestOnly.SpecStatusLabel(); got != "request_only" {
		t.Fatalf("request_only label=%q", got)
	}

	clean := loadSpecCall(t, "chat-service-run1-clean.json")
	if got := clean.SpecStatusLabel(); got != "clean" {
		t.Fatalf("clean label=%q", got)
	}

	absent := loadSpecCall(t, "not-checked.json")
	if got := absent.SpecStatusLabel(); got != "not checked" {
		t.Fatalf("absent label=%q", got)
	}

	skipped := Call{Normalized: Normalized{
		Validation: &Validation{SkippedReason: "passthrough"},
	}}
	if got := skipped.SpecStatusLabel(); got != "skipped:passthrough" {
		t.Fatalf("skipped label=%q", got)
	}
}

func TestSummaryOmitsStatusZeroOnAbort(t *testing.T) {
	trace := Trace{SessionID: "abort", Calls: []Call{{
		ID:         "call_a",
		Seq:        1,
		HTTPStatus: 0,
		Normalized: Normalized{
			FinishReason:   "cancelled",
			ClientAborted:  true,
			Validation:     &Validation{RequestChecked: true, ResponseChecked: false},
			SpecViolations: &[]SpecViolation{},
		},
	}}}
	summary := trace.Summary()
	if strings.Contains(summary, "status=0") || strings.Contains(summary, "http=0") {
		t.Fatalf("abort summary should omit status=0:\n%s", summary)
	}
	if !strings.Contains(summary, "spec=request_only") || !strings.Contains(summary, "client_aborted") {
		t.Fatalf("abort summary:\n%s", summary)
	}
}

func TestSpecFixturesUseGenericServiceNames(t *testing.T) {
	clean := loadSpecCall(t, "chat-service-run1-clean.json")
	if clean.SessionID != "chat-service-run1" {
		t.Fatalf("session_id=%q", clean.SessionID)
	}
	if _, err := os.Stat(filepath.Join("testdata", "spec", "chat"+"service-run1-clean.json")); err == nil {
		t.Fatal("legacy private fixture filename must be gone")
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "spec"))
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"heal" + "sens", "live" + "kit", "chat" + "service"}
	for _, e := range entries {
		lower := strings.ToLower(e.Name())
		for _, needle := range banned {
			if strings.Contains(lower, needle) {
				t.Fatalf("testdata/spec/%s still uses private name %q", e.Name(), needle)
			}
		}
		data, err := os.ReadFile(filepath.Join("testdata", "spec", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		body := strings.ToLower(string(data))
		for _, needle := range banned {
			if strings.Contains(body, needle) {
				t.Fatalf("testdata/spec/%s content still uses private name %q", e.Name(), needle)
			}
		}
	}
}

func TestProxyConfigUnmarshalUpstreams(t *testing.T) {
	var cfg ProxyConfig
	if err := json.Unmarshal([]byte(`{"version":"0.2.0","upstreams":{"openai":"https://api.openai.com/v1"}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "0.2.0" || cfg.Upstreams["openai"] != "https://api.openai.com/v1" {
		t.Fatalf("cfg=%+v", cfg)
	}
	var cfg2 ProxyConfig
	if err := json.Unmarshal([]byte(`{"version":"devel","upstreams":[{"name":"openai","url":"https://api.openai.com/v1"}]}`), &cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2.Upstreams["openai"] != "https://api.openai.com/v1" {
		t.Fatalf("list cfg=%+v", cfg2)
	}
}
