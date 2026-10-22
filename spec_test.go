// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"os"
	"os/exec"
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

func TestNoIgnoredFieldsSideUsesContext(t *testing.T) {
	contextOnly := Trace{SessionID: "s", Calls: []Call{loadSpecCall(t, "ignored-fields-context.json")}}
	if fields, ok := contextOnly.Calls[0].IgnoredFields(); !ok || len(fields) != 1 || fields[0].FilterSide() != "request" {
		t.Fatalf("context-only FilterSide=%#v", contextOnly.Calls[0].Normalized.IgnoredFields)
	}

	tb := &fakeTB{}
	(&Assertions{t: tb, trace: contextOnly}).NoIgnoredFields(Side("request"))
	joined := strings.Join(tb.messages, "\n")
	if !tb.failed || !strings.Contains(joined, "request") || !strings.Contains(joined, "vendor_field") || !strings.Contains(joined, "provider") {
		t.Fatalf("context-only request filter must fail: %q", tb.messages)
	}

	if !(&Assertions{t: t, trace: contextOnly, fatal: true}).NoIgnoredFields(Side("response")) {
		t.Fatal("request-context entry must not match Side(response)")
	}

	sideOnly := ignoredFieldTrace(`{"side":"request","kind":"vendor_field","path":"/foo","message":"side only"}`)
	tb = &fakeTB{}
	(&Assertions{t: tb, trace: sideOnly}).NoIgnoredFields(Side("request"))
	if !strings.Contains(strings.Join(tb.messages, "\n"), "request") {
		t.Fatalf("side-only request filter must fail: %q", tb.messages)
	}

	both := ignoredFieldTrace(`{"side":"response","context":"request","kind":"vendor_field","path":"/bar","message":"prefer context"}`)
	tb = &fakeTB{}
	(&Assertions{t: tb, trace: both}).NoIgnoredFields(Side("request"))
	if !strings.Contains(strings.Join(tb.messages, "\n"), "request") {
		t.Fatalf("context must win over side: %q", tb.messages)
	}
	if !(&Assertions{t: t, trace: both, fatal: true}).NoIgnoredFields(Side("response")) {
		t.Fatal("context=request must not match Side(response) even if side=response")
	}

	neither := ignoredFieldTrace(`{"kind":"vendor_field","path":"/bare","message":"no side"}`)
	tb = &fakeTB{}
	(&Assertions{t: tb, trace: neither}).NoIgnoredFields(Side("request"))
	if !strings.Contains(strings.Join(tb.messages, "\n"), "bare") {
		t.Fatalf("missing side/context must fail-closed: %q", tb.messages)
	}
	tb = &fakeTB{}
	(&Assertions{t: tb, trace: neither}).NoIgnoredFields(Side("response"))
	if !strings.Contains(strings.Join(tb.messages, "\n"), "bare") {
		t.Fatalf("missing side/context must match any Side filter: %q", tb.messages)
	}
}

func TestNoSpecViolationsMissingSideFailClosed(t *testing.T) {
	trace := Trace{SessionID: "s", Calls: []Call{{
		Seq: 1, APIFamily: "openai.chat",
		Normalized: Normalized{
			Validation:     &Validation{RequestChecked: true, ResponseChecked: true},
			SpecViolations: &[]SpecViolation{{Path: "/unk", Keyword: "unknown_request_field", Message: "no side"}},
		},
	}}}
	tb := &fakeTB{}
	(&Assertions{t: tb, trace: trace}).NoSpecViolations(Side("request"))
	if !strings.Contains(strings.Join(tb.messages, "\n"), "unk") {
		t.Fatalf("missing spec_violations.side must fail-closed: %q", tb.messages)
	}
	tb = &fakeTB{}
	(&Assertions{t: tb, trace: trace}).NoSpecViolations(Side("response"))
	if !strings.Contains(strings.Join(tb.messages, "\n"), "unk") {
		t.Fatalf("missing spec_violations.side must match any Side filter: %q", tb.messages)
	}
}

func TestSessionSpecCountsAliasesAndAbsent(t *testing.T) {
	var snake SessionSpecCounts
	if err := json.Unmarshal([]byte(`{"checked":3,"clean":1,"findings":2,"request_only":1,"not_checked":4}`), &snake); err != nil {
		t.Fatal(err)
	}
	if snake.Checked != 3 || snake.Clean != 1 || snake.Findings != 2 || snake.RequestOnly != 1 || snake.NotChecked != 4 {
		t.Fatalf("snake=%+v", snake)
	}
	var pascal SessionSpecCounts
	if err := json.Unmarshal([]byte(`{"Checked":3,"Clean":1,"Findings":2,"RequestOnly":1,"NotChecked":4}`), &pascal); err != nil {
		t.Fatal(err)
	}
	if pascal != snake {
		t.Fatalf("pascal=%+v snake=%+v", pascal, snake)
	}
	var preferSnake SessionSpecCounts
	if err := json.Unmarshal([]byte(`{"checked":9,"Checked":1,"clean":8,"Clean":0}`), &preferSnake); err != nil {
		t.Fatal(err)
	}
	if preferSnake.Checked != 9 || preferSnake.Clean != 8 {
		t.Fatalf("prefer snake=%+v", preferSnake)
	}
	var info SessionInfo
	if err := json.Unmarshal([]byte(`{"id":"s","calls":1,"active":0}`), &info); err != nil {
		t.Fatal(err)
	}
	if info.Spec != nil {
		t.Fatalf("absent spec=%+v", info.Spec)
	}
	if err := json.Unmarshal([]byte(`{"id":"s","calls":1,"active":0,"spec":{"checked":0,"clean":0,"findings":0,"request_only":0,"not_checked":1}}`), &info); err != nil {
		t.Fatal(err)
	}
	if info.Spec == nil || info.Spec.NotChecked != 1 || info.Spec.Checked != 0 {
		t.Fatalf("zero spec=%+v", info.Spec)
	}
}

func ignoredFieldTrace(entry string) Trace {
	var field IgnoredField
	if err := json.Unmarshal([]byte(entry), &field); err != nil {
		panic(err)
	}
	return Trace{SessionID: "s", Calls: []Call{{
		Seq: 1, APIFamily: "openai.chat",
		Normalized: Normalized{
			Validation:    &Validation{RequestChecked: true, ResponseChecked: true},
			IgnoredFields: &[]IgnoredField{field},
		},
	}}}
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
	cmd := exec.Command("bash", filepath.Join("scripts", "check-private-names.sh"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check-private-names.sh: %v\n%s", err, out)
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
