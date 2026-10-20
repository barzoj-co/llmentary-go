// SPDX-License-Identifier: MIT

package example

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// TestWaitQuietHonoured talks to the real bundled proxy. After one finished
// call, wait with quiet=100ms must return in < 400ms. That fails if the
// client still used the 500ms default (or sent a name the proxy ignores).
func TestWaitQuietHonoured(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_wait",
			"model":"gpt-fixture",
			"choices":[{
				"message":{"role":"assistant","content":"ok"},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}
		}`))
	}))
	t.Cleanup(upstream.Close)

	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithStartTimeout(10*time.Second),
	)

	lm := llmentary.New(t,
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
		llmentary.WithWait(100*time.Millisecond, 5*time.Second),
	)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(lm.OpenAIBaseURL("openai")),
		option.WithHTTPClient(lm.HTTPClient()),
		option.WithMaxRetries(0),
	)

	_, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "gpt-fixture",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("ping"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	trace := lm.Wait(ctx)
	elapsed := time.Since(start)
	if elapsed >= 400*time.Millisecond {
		t.Fatalf("wait with quiet=100ms took %s, want < 400ms (500ms default would fail this)", elapsed)
	}
	if len(trace.Calls) != 1 {
		t.Fatalf("want 1 recorded call after wait, got %d:\n%s", len(trace.Calls), trace.Summary())
	}
}
