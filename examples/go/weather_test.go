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

func TestWeatherTool(t *testing.T) {
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Skip(err.Error())
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_example",
			"model":"gpt-fixture",
			"choices":[{
				"message":{"role":"assistant","tool_calls":[{
					"id":"call_weather",
					"type":"function",
					"function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}
				}]},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}
		}`))
	}))
	t.Cleanup(upstream.Close)

	proxy := llmentary.StartForTest(t,
		llmentary.WithUpstream("openai="+upstream.URL),
		llmentary.WithStartTimeout(10*time.Second),
	)

	testLab := llmentary.New(t,
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
		llmentary.WithWait(time.Millisecond, time.Second),
	)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(testLab.OpenAIBaseURL("openai")),
		option.WithHTTPClient(testLab.HTTPClient()),
		option.WithMaxRetries(0),
	)

	_, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "gpt-fixture",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Weather in Paris?"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	assert := testLab.Expect(context.Background())
	assert.ToolCalled("get_weather", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
	assert.FinishReason("tool_calls")
	assert.TokensAtMost(20)
	assert.NoErrors()
}
