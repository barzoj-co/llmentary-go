// SPDX-License-Identifier: MIT

package example

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func TestPathBaseURLUnmodifiedSDK(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}

	upstream := mockChatUpstream(t)
	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithStartTimeout(10*time.Second),
	)
	id := llmentary.NewSessionID()
	pathURL := strings.TrimRight(proxy.ProxyURL, "/") + "/s/" + id + "/v1"
	t.Setenv("OPENAI_BASE_URL", pathURL)

	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithMaxRetries(0),
	)

	lm := llmentary.New(t,
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
		llmentary.WithSessionID(id),
		llmentary.WithWait(50*time.Millisecond, 5*time.Second),
	)
	if got := lm.PathBaseURL(""); got != pathURL {
		t.Fatalf("PathBaseURL=%q want %q", got, pathURL)
	}

	_, err := client.Chat.Completions.New(context.Background(), pingParams())
	if err != nil {
		t.Fatal(err)
	}

	trace := lm.Wait(context.Background())
	if len(trace.Calls) != 1 {
		t.Fatalf("calls=%d\n%s", len(trace.Calls), trace.Summary())
	}
	if trace.Calls[0].SessionID != id {
		t.Fatalf("session=%q want %q", trace.Calls[0].SessionID, id)
	}
	if src := trace.Calls[0].SessionSource; src != "" && src != "path" {
		t.Fatalf("session_source=%q", src)
	}
}

func TestMarkSinceTwoTurns(t *testing.T) {
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
	oa := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(lm.OpenAIBaseURL("openai")),
		option.WithHTTPClient(lm.HTTPClient()),
		option.WithMaxRetries(0),
	)

	for i := 0; i < 2; i++ {
		if _, err := oa.Chat.Completions.New(context.Background(), pingParams()); err != nil {
			t.Fatal(err)
		}
	}
	mark := lm.Mark(context.Background())
	if mark.Seq != 2 {
		t.Fatalf("mark.Seq=%d want 2\n%s", mark.Seq, lm.Calls(context.Background()).Summary())
	}

	for i := 0; i < 2; i++ {
		if _, err := oa.Chat.Completions.New(context.Background(), pingParams()); err != nil {
			t.Fatal(err)
		}
	}

	assert := lm.Expect(context.Background(), llmentary.Since(mark))
	if assert.Trace().Query().Len() != 2 {
		t.Fatalf("since mark want 2 calls, got %d\n%s", assert.Trace().Query().Len(), assert.Trace().Summary())
	}
	assert.CallCount(2)
	for _, call := range assert.Trace().Calls {
		if call.Seq <= mark.Seq {
			t.Fatalf("since leaked seq %d (mark %d)", call.Seq, mark.Seq)
		}
	}

	full := lm.Calls(context.Background())
	if full.Query().Since(mark).Len() != 2 {
		t.Fatalf("query.Since want 2, got %d", full.Query().Since(mark).Len())
	}
}

func TestSDKEnvOpenAIBaseURL(t *testing.T) {
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
	t.Setenv("OPENAI_BASE_URL", lm.OpenAIBaseURL("openai"))
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithHTTPClient(lm.HTTPClient()),
		option.WithMaxRetries(0),
	)
	if _, err := client.Chat.Completions.New(context.Background(), pingParams()); err != nil {
		t.Fatal(err)
	}
	trace := lm.Wait(context.Background())
	if len(trace.Calls) != 1 {
		t.Fatalf("env-constructed client calls=%d\n%s", len(trace.Calls), trace.Summary())
	}
}

func TestUnknownPrefixNotForwarded(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(upstream.Close)
	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithStartTimeout(10*time.Second),
	)
	resp, err := http.Post(proxy.ProxyURL+"/dogfood/v1/chat/completions", "application/json", strings.NewReader(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("unknown prefix must not be forwarded, status=200 body=%s", body)
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream hits=%d, want 0", hits.Load())
	}
}
