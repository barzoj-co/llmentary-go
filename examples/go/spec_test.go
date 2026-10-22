// SPDX-License-Identifier: MIT

package example

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary-go"
)

func TestSpecViolationsAgainstProxy(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}

	upstream := mockChatUpstream(t)
	// Localhost defaults to openai-compatible in v0.5.0; pin the closed-world
	// OpenAI target so extra request fields stay spec_violations.
	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithSpecTarget("openai="+llmentary.SpecTargetOpenAI),
		llmentary.WithStartTimeout(10*time.Second),
	)
	lm := llmentary.New(t,
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
		llmentary.WithWait(50*time.Millisecond, 5*time.Second),
	)
	trace := postUnknownFieldChat(t, lm)
	if trace.Calls[0].SpecTarget() != llmentary.SpecTargetOpenAI {
		t.Fatalf("spec target=%q", trace.Calls[0].SpecTarget())
	}
	findings, checked := trace.Calls[0].SpecViolations()
	if !checked {
		t.Fatal("proxy did not record spec_violations; need proxy v0.2.0+")
	}
	if len(findings) == 0 {
		t.Fatal("expected a request violation for top-level foo")
	}

	tb := &recordTB{}
	trace.Query().Check(tb).NoSpecViolations()
	tb.flush()
	if !tb.failed {
		t.Fatal("NoSpecViolations should fail on foo")
	}
	joined := strings.Join(tb.messages, "\n")
	for _, want := range []string{"#1", "request", "foo", "unevaluatedProperties"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("contract message missing %q:\n%s", want, joined)
		}
	}
}

func TestCompatibleTargetRecordsVendorField(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}

	upstream := mockChatUpstream(t)
	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithStartTimeout(10*time.Second),
	)
	lm := llmentary.New(t,
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
		llmentary.WithWait(50*time.Millisecond, 5*time.Second),
	)
	trace := postUnknownFieldChat(t, lm)
	if trace.Calls[0].SpecTarget() != llmentary.SpecTargetOpenAICompatible {
		t.Fatalf("localhost default spec target=%q", trace.Calls[0].SpecTarget())
	}
	findings, checked := trace.Calls[0].SpecViolations()
	if !checked {
		t.Fatal("compatible target still records spec_violations")
	}
	for _, finding := range findings {
		if strings.Contains(finding.Path, "foo") || strings.Contains(finding.Pointer, "foo") || strings.Contains(finding.Message, "foo") {
			t.Fatalf("openai-compatible must not reject request foo: %+v", finding)
		}
	}
	ignored, ok := trace.Calls[0].IgnoredFields()
	if !ok || len(ignored) == 0 {
		t.Fatal("expected ignored_fields for vendor extra foo")
	}
	sideTB := &recordTB{}
	trace.Query().Check(sideTB).NoIgnoredFields(llmentary.Side("request"))
	sideTB.flush()
	if !sideTB.failed {
		t.Fatal("NoIgnoredFields(Side(request)) must fail on request vendor_field (DOG-45)")
	}
	joinedSide := strings.Join(sideTB.messages, "\n")
	if !strings.Contains(joinedSide, "request") || !strings.Contains(joinedSide, "foo") {
		t.Fatalf("request-side ignored-field message=%q", joinedSide)
	}
	found := false
	for _, field := range ignored {
		if field.Kind == "vendor_field" && (strings.Contains(field.Path, "foo") || strings.Contains(field.Pointer, "foo") || strings.Contains(field.Message, "foo")) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ignored_fields=%#v", ignored)
	}
}

func postUnknownFieldChat(t *testing.T, lm *llmentary.Session) llmentary.Trace {
	t.Helper()
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"foo":1}`)
	req, err := http.NewRequest(http.MethodPost, lm.OpenAIBaseURL("openai")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	key, value := lm.Header()
	req.Header.Set(key, value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	trace := lm.Wait(context.Background())
	if len(trace.Calls) != 1 {
		t.Fatalf("calls=%d\n%s", len(trace.Calls), trace.Summary())
	}
	return trace
}

type recordTB struct {
	testing.TB
	messages []string
	failed   bool
	cleanups []func()
}

func (f *recordTB) Helper() {}

func (f *recordTB) Cleanup(fn func()) {
	f.cleanups = append(f.cleanups, fn)
}

func (f *recordTB) flush() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	f.cleanups = nil
}

func (f *recordTB) Error(args ...any) {
	f.failed = true
	f.messages = append(f.messages, fmt.Sprint(args...))
}

func (f *recordTB) Errorf(format string, args ...any) {
	f.failed = true
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}

func (f *recordTB) Fatal(args ...any) { f.Error(args...) }

func (f *recordTB) Fatalf(format string, args ...any) { f.Errorf(format, args...) }
