// SPDX-License-Identifier: MIT

package example

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestCompatibleRecipeStreamAndNonStream(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}

	upstream := mockChatUpstreamStreamAware(t)
	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithStartTimeout(10*time.Second),
	)

	for _, stream := range []bool{false, true} {
		lm := llmentary.New(t,
			llmentary.WithProxyURL(proxy.ProxyURL),
			llmentary.WithControlURL(proxy.ControlURL),
			llmentary.WithWait(50*time.Millisecond, 5*time.Second),
		)
		trace := postChat(t, lm, stream, false)
		if trace.Calls[0].SpecTarget() != llmentary.SpecTargetOpenAICompatible {
			t.Fatalf("stream=%v spec target=%q", stream, trace.Calls[0].SpecTarget())
		}
		if !trace.Query().Check(t).NoSpecViolations() {
			t.Fatalf("stream=%v NoSpecViolations should pass", stream)
		}
		if !trace.Query().Check(t).NoIgnoredFields(llmentary.Side("request")) {
			t.Fatalf("stream=%v Side(request) recipe should pass without vendor extras", stream)
		}
		if !trace.Query().Check(t).NoIgnoredFields(llmentary.MinSeverity("warning")) {
			t.Fatalf("stream=%v MinSeverity(warning) should drop info compatible_optional", stream)
		}
		if !stream {
			tb := &recordTB{}
			trace.Query().Check(tb).NoIgnoredFields()
			tb.flush()
			if !tb.failed || !strings.Contains(strings.Join(tb.messages, "\n"), "compatible_optional") {
				t.Fatalf("non-stream default NoIgnoredFields must fail on compatible_optional: %q", tb.messages)
			}
		}
	}

	lm := llmentary.New(t,
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
		llmentary.WithWait(50*time.Millisecond, 5*time.Second),
	)
	vendor := postChat(t, lm, false, true)
	tb := &recordTB{}
	vendor.Query().Check(tb).NoIgnoredFields(llmentary.Side("request"))
	tb.flush()
	if !tb.failed || !strings.Contains(strings.Join(tb.messages, "\n"), "foo") {
		t.Fatalf("Side(request) recipe must fail on vendor extra: %q", tb.messages)
	}
}

func mockChatUpstreamStreamAware(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_stream\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_ctrl",
			"model":"gpt-fixture",
			"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func postChat(t *testing.T, lm *llmentary.Session, stream, vendorExtra bool) llmentary.Trace {
	t.Helper()
	extra := ""
	if vendorExtra {
		extra = `,"foo":1`
	}
	body := []byte(fmt.Sprintf(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":%t%s}`, stream, extra))
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
		t.Fatalf("stream=%v vendor=%v calls=%d\n%s", stream, vendorExtra, len(trace.Calls), trace.Summary())
	}
	return trace
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
