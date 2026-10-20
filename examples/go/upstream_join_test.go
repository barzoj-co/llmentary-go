// SPDX-License-Identifier: MIT

package example

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary-go"
)

// Same (upstream setting, base URL) → final upstream URL table used by the
// Python and JS clients. Paths are what the echo upstream receives.
func TestUpstreamJoinTable(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}

	type row struct {
		name     string
		upstream string // path on the echo server, empty host filled in
		base     string // "v1", "u", or "root"
		reqPath  string
		wantPath string
	}
	rows := []row{
		{name: "v1-base + /v1 client", upstream: "/v1", base: "v1", reqPath: "/chat/completions", wantPath: "/v1/chat/completions"},
		{name: "bare host + /v1 client", upstream: "", base: "v1", reqPath: "/chat/completions", wantPath: "/v1/chat/completions"},
		{name: "v1-base + /u/openai", upstream: "/v1", base: "u", reqPath: "/chat/completions", wantPath: "/v1/chat/completions"},
		{name: "api/v1 + /v1 client", upstream: "/api/v1", base: "v1", reqPath: "/chat/completions", wantPath: "/api/v1/chat/completions"},
		{name: "api/v1 + /u/openai", upstream: "/api/v1", base: "u", reqPath: "/chat/completions", wantPath: "/api/v1/chat/completions"},
		{name: "api/v1 + root /chat", upstream: "/api/v1", base: "root", reqPath: "/chat/completions", wantPath: "/api/v1/chat/completions"},
		{name: "openai/v1 + /v1 responses", upstream: "/openai/v1", base: "v1", reqPath: "/responses", wantPath: "/openai/v1/responses"},
		{name: "openai/v1 + /u responses", upstream: "/openai/v1", base: "u", reqPath: "/responses", wantPath: "/openai/v1/responses"},
		{name: "v1-base + /v1 embeddings", upstream: "/v1", base: "v1", reqPath: "/embeddings", wantPath: "/v1/embeddings"},
		{name: "api/v1 + /u embeddings", upstream: "/api/v1", base: "u", reqPath: "/embeddings", wantPath: "/api/v1/embeddings"},
		{name: "azure deploy + /u chat", upstream: "/openai/deployments/dep", base: "u", reqPath: "/chat/completions", wantPath: "/openai/deployments/dep/chat/completions"},
		{name: "azure deploy/v1 overlap", upstream: "/openai/deployments/dep/v1", base: "v1", reqPath: "/chat/completions", wantPath: "/openai/deployments/dep/v1/chat/completions"},
		{name: "v1-base + /u models", upstream: "/v1", base: "u", reqPath: "/models", wantPath: "/v1/models"},
		{name: "root upstream + /v1 chat", upstream: "/", base: "v1", reqPath: "/chat/completions", wantPath: "/v1/chat/completions"},
		{name: "openrouter-style /api/v1 + /v1", upstream: "/api/v1", base: "v1", reqPath: "/chat/completions", wantPath: "/api/v1/chat/completions"},
	}

	var mu sync.Mutex
	var seen []string
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"echo","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(echo.Close)

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			seen = nil
			mu.Unlock()

			up := strings.TrimRight(echo.URL, "/") + tc.upstream
			if tc.upstream == "/" {
				up = echo.URL
			}
			proxy := llmentary.StartForTest(t,
				llmentary.WithUpstream("openai="+up),
				llmentary.WithStartTimeout(10*time.Second),
			)
			lm := llmentary.New(t,
				llmentary.WithProxyURL(proxy.ProxyURL),
				llmentary.WithControlURL(proxy.ControlURL),
				llmentary.WithWait(20*time.Millisecond, 3*time.Second),
			)
			base := lm.OpenAIBaseURL("")
			switch tc.base {
			case "u":
				base = lm.OpenAIBaseURL("openai")
			case "root":
				base = lm.BaseURL("")
			}
			reqPath := tc.reqPath
			url := strings.TrimRight(base, "/") + reqPath
			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`)))
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

			mu.Lock()
			got := append([]string(nil), seen...)
			mu.Unlock()
			if len(got) == 0 {
				t.Fatalf("echo received no request (status=%d). proxy too old for overlap join?", resp.StatusCode)
			}
			last := got[len(got)-1]
			if last != tc.wantPath {
				t.Fatalf("upstream path=%q want %q (status=%d)", last, tc.wantPath, resp.StatusCode)
			}
		})
	}
}
