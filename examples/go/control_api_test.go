// SPDX-License-Identifier: MIT

package example

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func TestListSessionsMinCallsAndNotFound(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}

	upstream := mockChatUpstream(t)
	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithStartTimeout(10*time.Second),
	)
	client, err := llmentary.NewClient(proxy.ControlURL)
	if err != nil {
		t.Fatal(err)
	}

	version, err := client.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(version) == "" {
		t.Fatal("GET /config version is empty; need proxy v0.2.0+")
	}
	t.Logf("proxy version %s", version)

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

	done := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, err := oa.Chat.Completions.New(context.Background(), pingParams())
		if err != nil {
			done <- err
			return
		}
		_, err = oa.Chat.Completions.New(context.Background(), pingParams())
		done <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	trace := lm.Wait(ctx, llmentary.MinCalls(2))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(trace.Calls) != 2 {
		t.Fatalf("min_calls=2 got %d calls:\n%s", len(trace.Calls), trace.Summary())
	}

	list, err := client.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	var ids []string
	for _, item := range list {
		ids = append(ids, item.ID)
		if item.ID == lm.SessionID() {
			found = true
			if item.Calls != 2 {
				t.Fatalf("list row for %s has calls=%d", item.ID, item.Calls)
			}
		}
	}
	if !found {
		t.Fatalf("ListSessions missing %s (known %v)", lm.SessionID(), ids)
	}

	_, err = client.Calls(ctx, "definitely-missing")
	if !llmentary.IsSessionNotFound(err) {
		t.Fatalf("unknown session err=%v (proxy too old?)", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "no calls recorded for session definitely-missing") {
		t.Fatalf("SessionNotFound message=%q", msg)
	}
	if !strings.Contains(msg, lm.SessionID()) {
		t.Fatalf("zero-calls / unknown-session message should list known ids %q, got %q", lm.SessionID(), msg)
	}

	empty := llmentary.New(t,
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
		llmentary.WithWait(50*time.Millisecond, 2*time.Second),
	)
	_, err = client.Wait(ctx, empty.SessionID(), 50*time.Millisecond, 2*time.Second, llmentary.MinCalls(1))
	if llmentary.IsSessionNotFound(err) {
		t.Fatalf("min_calls>=1 on a never-created session must be a timeout, got SessionNotFound: %v", err)
	}
	if !llmentary.IsWaitTimeout(err) {
		t.Fatalf("zero-call wait min_calls=1 err=%v", err)
	}
	if !strings.Contains(err.Error(), empty.SessionID()) || !strings.Contains(err.Error(), "wait timed out") {
		t.Fatalf("timeout %q should name the empty session", err)
	}
}

func TestDefaultUpstreamConfig(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}
	proxy := llmentary.StartForTest(t, llmentary.WithStartTimeout(10*time.Second))
	client, err := llmentary.NewClient(proxy.ControlURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := client.Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version == "" {
		t.Fatal("default-upstream proxy has empty version")
	}
	got := ""
	for name, u := range cfg.Upstreams {
		if name == "openai" || strings.Contains(u, "api.openai.com") {
			got = u
			break
		}
	}
	if !strings.Contains(got, "api.openai.com") {
		t.Fatalf("default upstreams=%v, want openai=https://api.openai.com/v1", cfg.Upstreams)
	}
	if strings.Count(got, "/v1") > 1 {
		t.Fatalf("default upstream URL looks doubled: %s", got)
	}
}

func mockChatUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func pingParams() openai.ChatCompletionNewParams {
	return openai.ChatCompletionNewParams{
		Model: "gpt-fixture",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("ping"),
		},
	}
}
