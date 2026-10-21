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
	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithStartTimeout(10*time.Second),
	)
	lm := llmentary.New(t,
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
		llmentary.WithWait(50*time.Millisecond, 5*time.Second),
	)

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
	findings, checked := trace.Calls[0].SpecViolations()
	if !checked {
		t.Fatal("proxy did not record spec_violations; need proxy v0.2.0+")
	}
	if len(findings) == 0 {
		t.Fatal("expected a request violation for top-level foo")
	}

	tb := &recordTB{}
	trace.Query().Check(tb).NoSpecViolations()
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

type recordTB struct {
	testing.TB
	messages []string
	failed   bool
}

func (f *recordTB) Helper() {}

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
