// SPDX-License-Identifier: MIT

//go:build e2e

package llmentary_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

func TestE2EChatCompletionsToolFlow(t *testing.T) {
	apiKey := requireOpenAIKey(t)
	model := e2eModel()
	lm := newSession(t)
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(lm.OpenAIBaseURL("openai")),
		option.WithHTTPClient(lm.HTTPClient()),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	params := openai.ChatCompletionNewParams{
		Model:       model,
		Temperature: param.NewOpt(0.0),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Call the echo_city tool with city Paris. Do not answer in text."),
		},
		Tools: []openai.ChatCompletionToolParam{echoCityChatTool()},
		ToolChoice: openai.ChatCompletionToolChoiceOptionParamOfChatCompletionNamedToolChoice(openai.ChatCompletionNamedToolChoiceFunctionParam{
			Name: "echo_city",
		}),
	}
	first, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Choices) == 0 || len(first.Choices[0].Message.ToolCalls) == 0 {
		t.Fatalf("expected chat tool call, got %#v", first)
	}
	toolCall := first.Choices[0].Message.ToolCalls[0]
	params.Messages = append(params.Messages, first.Choices[0].Message.ToParam())
	params.Messages = append(params.Messages, openai.ToolMessage(`{"city":"Paris","echoed":true}`, toolCall.ID))
	params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{}
	final, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Choices) == 0 || !strings.Contains(final.Choices[0].Message.Content, "Paris") {
		t.Fatalf("expected final chat answer to mention Paris, got %#v", final)
	}

	assert := lm.Expect(ctx)
	chat := assert.Trace().Query().APIType("chat").Expect(t)
	chat.CallCount(2)
	chat.ToolCalled("echo_city", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
	chat.ToolResultFedBack(toolCall.ID)
	chat.FinishReason("stop")
	chat.ResponseContains("Paris")
	chat.NoErrors()
	chat.TokensAtMost(2000)
	chat.TTFTBelow(10 * time.Second)
	chat.LatencyBelow(30 * time.Second)
	chat.NoRetries()
	assert.NoSpecViolations()
}

func TestE2EResponsesToolFlow(t *testing.T) {
	apiKey := requireOpenAIKey(t)
	model := e2eModel()
	lm := newSession(t)

	firstBody := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"input":"Call the echo_city tool with city Paris. Do not answer in text.",
		"stream":true,
		"temperature":0,
		"tool_choice":{"type":"function","name":"echo_city"},
		"tools":[{
			"type":"function",
			"name":"echo_city",
			"description":"Echo a city name.",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}
		}]
	}`)
	responsesStream := postOpenAI(t, apiKey, lm, "/responses", firstBody)
	if !bytes.Contains(responsesStream, []byte("event:")) {
		t.Fatalf("expected responses SSE stream, got %q", responsesStream)
	}
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 20*time.Second)
	firstTrace := lm.Wait(firstCtx)
	firstCancel()
	responseCalls := firstTrace.Query().APIType("responses").Calls()
	if len(responseCalls) != 1 || len(responseCalls[0].Normalized.ToolCalls) != 1 {
		t.Fatalf("expected one responses tool call:\n%s", firstTrace.Summary())
	}
	toolCallID := responseCalls[0].Normalized.ToolCalls[0].ID
	responseID := responseCalls[0].RequestIDs["body.id"]
	if responseID == "" {
		t.Fatalf("first Responses call did not record body.id:\n%s", firstTrace.Summary())
	}

	secondBody := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"stream":true,
		"previous_response_id":` + strconv.Quote(responseID) + `,
		"input":[
			{"type":"function_call_output","call_id":` + strconv.Quote(toolCallID) + `,"output":"{\"city\":\"Paris\",\"echoed\":true}"},
			{"role":"user","content":"Now give the final answer in text and include Paris."}
		],
		"tools":[{
			"type":"function",
			"name":"echo_city",
			"description":"Echo a city name.",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}
		}]
	}`)
	finalStream := postOpenAI(t, apiKey, lm, "/responses", secondBody)
	if !bytes.Contains(finalStream, []byte("Paris")) {
		t.Fatalf("expected final responses stream to mention Paris, got %q", finalStream)
	}

	finalCtx, finalCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer finalCancel()
	assert := lm.Expect(finalCtx)
	responses := assert.Trace().Query().APIType("responses").Expect(t)
	responses.CallCount(2)
	responses.ToolCalled("echo_city", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
	responses.ToolResultFedBack(toolCallID)
	responses.FinishReason("stop")
	responses.ResponseContains("Paris")
	responses.NoErrors()
	responses.TokensAtMost(2000)
	responses.TTFTBelow(10 * time.Second)
	responses.LatencyBelow(30 * time.Second)
	responses.NoRetries()
}

func TestE2EStreamingWaitDefaults(t *testing.T) {
	apiKey := requireOpenAIKey(t)
	model := e2eModel()
	lm := newSessionDefaults(t)
	body := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[{"role":"user","content":"Reply with the single word hi."}],
		"stream":true,
		"temperature":0
	}`)
	stream := postOpenAI(t, apiKey, lm, "/chat/completions", body)
	if !bytes.Contains(stream, []byte("data:")) {
		t.Fatalf("expected chat SSE stream, got %q", stream)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	trace := lm.Wait(ctx)
	if len(trace.Calls) < 1 {
		t.Fatalf("wait with default timeout/quiet returned no calls:\n%s", trace.Summary())
	}
}

func TestE2EWaitMinCalls(t *testing.T) {
	apiKey := requireOpenAIKey(t)
	model := e2eModel()
	lm := newSession(t)

	errc := make(chan error, 1)
	go func() {
		time.Sleep(1500 * time.Millisecond)
		chatBody := []byte(`{
			"model":` + strconv.Quote(model) + `,
			"messages":[{"role":"user","content":"Reply with the single word hi."}],
			"stream":true,
			"temperature":0
		}`)
		stream, err := doOpenAI(apiKey, lm, "/chat/completions", chatBody)
		if err != nil || !bytes.Contains(stream, []byte("data:")) {
			errc <- fmt.Errorf("chat stream: %w", err)
			return
		}
		respBody := []byte(`{
			"model":` + strconv.Quote(model) + `,
			"input":"Reply with the single word hi.",
			"stream":true,
			"temperature":0
		}`)
		stream, err = doOpenAI(apiKey, lm, "/responses", respBody)
		if err != nil || !bytes.Contains(stream, []byte("event:")) {
			errc <- fmt.Errorf("responses stream: %w", err)
			return
		}
		errc <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	trace := lm.Wait(ctx, llmentary.MinCalls(2))
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if len(trace.Calls) < 2 {
		t.Fatalf("min_calls=2 returned %d calls:\n%s", len(trace.Calls), trace.Summary())
	}
	for _, call := range trace.Calls {
		if call.Normalized.Usage == nil {
			t.Fatalf("call #%d missing usage:\n%s", call.Seq, trace.Summary())
		}
	}
}

func TestE2EDefaultUpstream(t *testing.T) {
	apiKey := requireOpenAIKey(t)
	model := e2eModel()
	lm := newSessionDefaultUpstream(t)
	body := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[{"role":"user","content":"Reply with the single word hi."}],
		"stream":true,
		"temperature":0
	}`)
	req, err := http.NewRequest(http.MethodPost, lm.OpenAIBaseURL("")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	key, value := lm.Header()
	req.Header.Set(key, value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("default-upstream openai status=%d", resp.StatusCode)
	}
	if !bytes.Contains(stream, []byte("data:")) {
		t.Fatalf("expected chat SSE stream from default upstream")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	trace := lm.Wait(ctx)
	if len(trace.Calls) < 1 {
		t.Fatalf("default upstream recorded no calls:\n%s", trace.Summary())
	}
	up := trace.Calls[0].Upstream.URL
	if strings.Contains(up, "/v1/v1/") {
		t.Fatalf("default upstream produced /v1/v1: %s", up)
	}
	if trace.Calls[0].Normalized.Usage == nil {
		t.Fatalf("default upstream call missing usage:\n%s", trace.Summary())
	}
}

func TestE2EUnknownNestedFieldSpecViolation(t *testing.T) {
	apiKey := requireOpenAIKey(t)
	model := e2eModel()
	lm := newSession(t)
	body := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[{"role":"user","content":"Reply with the single word hi.","llmentary_probe":true}],
		"stream":false,
		"temperature":0
	}`)
	req, err := http.NewRequest(http.MethodPost, lm.OpenAIBaseURL("openai")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	key, value := lm.Header()
	req.Header.Set(key, value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown nested field should be 200 upstream, got %d", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	trace := lm.Wait(ctx)
	tb := &e2eRecorder{TB: t}
	trace.Query().Check(tb).NoSpecViolations()
	if !tb.failed {
		t.Fatal("NoSpecViolations should fail for llmentary_probe")
	}
	joined := strings.Join(tb.messages, "\n")
	for _, want := range []string{"#1", "request", "llmentary_probe"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("contract message missing %q:\n%s", want, joined)
		}
	}
}

func TestE2EChatCompletionsNonStreaming(t *testing.T) {
	apiKey := requireOpenAIKey(t)
	model := e2eModel()
	lm := newSession(t)
	body := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[{"role":"user","content":"Call the echo_city tool with city Paris. Do not answer in text."}],
		"stream":false,
		"temperature":0,
		"tool_choice":{"type":"function","function":{"name":"echo_city"}},
		"tools":[{
			"type":"function",
			"function":{
				"name":"echo_city",
				"description":"Echo a city name.",
				"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}
			}
		}]
	}`)
	_ = postOpenAI(t, apiKey, lm, "/chat/completions", body)
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 20*time.Second)
	firstTrace := lm.Wait(firstCtx)
	firstCancel()
	chatCalls := firstTrace.Query().APIType("chat").Calls()
	if len(chatCalls) != 1 || len(chatCalls[0].Normalized.ToolCalls) != 1 {
		t.Fatalf("expected one chat tool call:\n%s", firstTrace.Summary())
	}
	toolCall := chatCalls[0].Normalized.ToolCalls[0]
	finalBody := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[
			{"role":"user","content":"Call the echo_city tool with city Paris. Do not answer in text."},
			{"role":"assistant","tool_calls":[{"id":` + strconv.Quote(toolCall.ID) + `,"type":"function","function":{"name":"echo_city","arguments":` + strconv.Quote(toolCall.ArgumentsRaw) + `}}]},
			{"role":"tool","tool_call_id":` + strconv.Quote(toolCall.ID) + `,"content":"{\"city\":\"Paris\",\"echoed\":true}"},
			{"role":"user","content":"Now give the final answer in text and include Paris."}
		],
		"temperature":0
	}`)
	_ = postOpenAI(t, apiKey, lm, "/chat/completions", finalBody)
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer finalCancel()
	assert := lm.Expect(finalCtx)
	assert.CallCount(2)
	chat := assert.Trace().Query().APIType("chat").Expect(t)
	chat.ToolCalled("echo_city", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
	chat.ToolResultFedBack(toolCall.ID)
	chat.FinishReason("stop")
	chat.ResponseContains("Paris")
	assert.NoErrors()
}

func requireOpenAIKey(t *testing.T) string {
	t.Helper()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Fatal("OPENAI_API_KEY is required for e2e tests (not skipped)")
	}
	return apiKey
}

func postOpenAI(t *testing.T, apiKey string, lm *llmentary.Session, path string, body []byte) []byte {
	t.Helper()
	stream, err := doOpenAI(apiKey, lm, path, body)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func doOpenAI(apiKey string, lm *llmentary.Session, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, lm.OpenAIBaseURL("openai")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	key, value := lm.Header()
	req.Header.Set(key, value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	stream, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return stream, fmt.Errorf("openai status=%d", resp.StatusCode)
	}
	return stream, nil
}

func newSession(t *testing.T) *llmentary.Session {
	t.Helper()
	return newSessionWithWait(t, llmentary.WithWait(200*time.Millisecond, 30*time.Second))
}

func newSessionDefaults(t *testing.T) *llmentary.Session {
	t.Helper()
	return newSessionWithWait(t)
}

func newSessionWithWait(t *testing.T, opts ...llmentary.Option) *llmentary.Session {
	t.Helper()
	return newSessionOnProxy(t, true, opts...)
}

func newSessionDefaultUpstream(t *testing.T) *llmentary.Session {
	t.Helper()
	return newSessionOnProxy(t, false, llmentary.WithWait(200*time.Millisecond, 30*time.Second))
}

func newSessionOnProxy(t *testing.T, namedUpstream bool, opts ...llmentary.Option) *llmentary.Session {
	t.Helper()
	if _, err := llmentary.ResolveBinary(); err != nil {
		t.Fatalf("llmentary proxy binary is required for e2e: %v", err)
	}
	startOpts := []llmentary.StartOption{llmentary.WithStartTimeout(15 * time.Second)}
	if namedUpstream {
		startOpts = append(startOpts, llmentary.WithUpstream("openai=https://api.openai.com/v1"))
	}
	proxy := llmentary.StartForTest(t, startOpts...)
	sessionOpts := []llmentary.Option{
		llmentary.WithProxyURL(proxy.ProxyURL),
		llmentary.WithControlURL(proxy.ControlURL),
	}
	sessionOpts = append(sessionOpts, opts...)
	return llmentary.New(t, sessionOpts...)
}

type e2eRecorder struct {
	testing.TB
	messages []string
	failed   bool
}

func (f *e2eRecorder) Helper() {}

func (f *e2eRecorder) Error(args ...any) {
	f.failed = true
	f.messages = append(f.messages, fmt.Sprint(args...))
}

func (f *e2eRecorder) Errorf(format string, args ...any) {
	f.failed = true
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}

func (f *e2eRecorder) Fatal(args ...any) { f.Error(args...) }

func (f *e2eRecorder) Fatalf(format string, args ...any) { f.Errorf(format, args...) }

func e2eModel() string {
	if model := os.Getenv("OPENAI_E2E_MODEL"); model != "" {
		return model
	}
	return "gpt-4.1-mini"
}

func echoCityChatTool() openai.ChatCompletionToolParam {
	return openai.ChatCompletionToolParam{
		Function: openai.FunctionDefinitionParam{
			Name:        "echo_city",
			Description: param.NewOpt("Echo a city name."),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required":             []string{"city"},
				"additionalProperties": false,
			},
		},
	}
}
