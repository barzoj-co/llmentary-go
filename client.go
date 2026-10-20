// SPDX-License-Identifier: MIT

package llmentary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type Client struct {
	baseURL      *url.URL
	httpClient   *http.Client
	controlToken string
}

func NewClient(proxyControlURL string, opts ...ClientOption) (*Client, error) {
	u, err := url.Parse(proxyControlURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("proxy control URL must include scheme and host")
	}
	c := &Client{baseURL: u, httpClient: http.DefaultClient, controlToken: os.Getenv("LLMENTARY_CONTROL_TOKEN")}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

func WithControlToken(token string) ClientOption {
	return func(c *Client) {
		c.controlToken = token
	}
}

type ClientOption func(*Client)

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func (c *Client) SessionHeader(sessionID string) (string, string) {
	return Header(sessionID)
}

func (c *Client) NewSession() string {
	return NewSessionID()
}

func (c *Client) Calls(ctx context.Context, sessionID string) ([]Call, error) {
	var session sessionPayload
	if err := c.getJSON(ctx, "/sessions/"+url.PathEscape(sessionID), nil, &session); err != nil {
		return nil, err
	}
	return session.Calls, nil
}

func (c *Client) Wait(ctx context.Context, sessionID string, quiet, timeout time.Duration) ([]Call, error) {
	httpTimeout := waitHTTPTimeout(quiet, timeout)
	waitCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	var session sessionPayload
	err := c.getJSONQuery(waitCtx, c.waitHTTPClient(httpTimeout), "/sessions/"+url.PathEscape(sessionID)+"/wait", waitQuery(quiet, timeout), &session)
	if err == nil {
		return session.Calls, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !isWaitTimeout(err) {
		return nil, err
	}
	calls := session.Calls
	if len(calls) == 0 {
		if parsed := parseSessionCalls(err); len(parsed) > 0 {
			calls = parsed
		} else if fetched, fetchErr := c.Calls(ctx, sessionID); fetchErr == nil {
			calls = fetched
		}
	}
	return nil, waitTimeoutError(sessionID, timeout, calls)
}

func (c *Client) OTLP(ctx context.Context, sessionID string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.getJSON(ctx, "/sessions/"+url.PathEscape(sessionID)+"/otlp", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) Delete(ctx context.Context, sessionID string) error {
	u := c.resolve("/sessions/" + url.PathEscape(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("llmentary delete failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return nil
}

type statusError struct {
	op     string
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("llmentary %s failed: status=%d body=%s", e.op, e.status, e.body)
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	raw := ""
	if len(q) > 0 {
		raw = q.Encode()
	}
	return c.getJSONQuery(ctx, c.httpClient, path, raw, out)
}

func (c *Client) getJSONQuery(ctx context.Context, hc *http.Client, path, rawQuery string, out any) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	u := c.resolve(path)
	u.RawQuery = rawQuery
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &statusError{op: "get", status: resp.StatusCode, body: string(body)}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) waitHTTPClient(needed time.Duration) *http.Client {
	base := c.httpClient
	if base == nil {
		base = http.DefaultClient
	}
	if base.Timeout == 0 || base.Timeout >= needed {
		return base
	}
	return &http.Client{
		Transport:     base.Transport,
		CheckRedirect: base.CheckRedirect,
		Jar:           base.Jar,
		Timeout:       needed,
	}
}

func isWaitTimeout(err error) bool {
	var se *statusError
	if errors.As(err, &se) && se.status == http.StatusGatewayTimeout {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func parseSessionCalls(err error) []Call {
	var se *statusError
	if !errors.As(err, &se) || se.body == "" {
		return nil
	}
	var payload sessionPayload
	if json.Unmarshal([]byte(se.body), &payload) != nil {
		return nil
	}
	return payload.Calls
}

func (c *Client) resolve(path string) *url.URL {
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	return &u
}

func (c *Client) authorize(req *http.Request) {
	if c.controlToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.controlToken)
	}
}

func AssertToolCalled(t testing.TB, calls []Call, name string, wantArgs any) {
	t.Helper()
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			if toolCall.Name != name {
				continue
			}
			if wantArgs == nil || jsonMatches(t, toolCall.Arguments, wantArgs) {
				return
			}
		}
	}
	t.Fatalf("expected tool %q to be called with args %s; calls=%d", name, mustJSON(wantArgs), len(calls))
}

func ToolCalled(t testing.TB, calls []Call, name string, wantArgs any) {
	t.Helper()
	AssertToolCalled(t, calls, name, wantArgs)
}

func AssertToolNotCalled(t testing.TB, calls []Call, name string) {
	t.Helper()
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			if toolCall.Name == name {
				t.Fatalf("expected tool %q not to be called; got args=%s in call=%s", name, string(toolCall.Arguments), call.ID)
			}
		}
	}
}

func ToolNotCalled(t testing.TB, calls []Call, name string) {
	t.Helper()
	AssertToolNotCalled(t, calls, name)
}

func AssertToolOrder(t testing.TB, calls []Call, names ...string) {
	t.Helper()
	got := make([]string, 0)
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			got = append(got, toolCall.Name)
		}
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("tool order mismatch: got=%v want=%v", got, names)
	}
}

func ToolOrder(t testing.TB, calls []Call, names ...string) {
	t.Helper()
	AssertToolOrder(t, calls, names...)
}

type CallCountOptions struct {
	ExcludeRetries bool
}

type CallCountOption func(*CallCountOptions)

func ExcludeRetries() CallCountOption {
	return func(o *CallCountOptions) {
		o.ExcludeRetries = true
	}
}

func AssertCallCount(t testing.TB, calls []Call, want int, opts ...CallCountOption) {
	t.Helper()
	var options CallCountOptions
	for _, opt := range opts {
		opt(&options)
	}
	got := 0
	for _, call := range calls {
		if options.ExcludeRetries && call.RetryCount != nil && *call.RetryCount > 0 {
			continue
		}
		got++
	}
	if got != want {
		t.Fatalf("call count mismatch: got=%d want=%d", got, want)
	}
}

func CallCount(t testing.TB, calls []Call, want int, opts ...CallCountOption) {
	t.Helper()
	AssertCallCount(t, calls, want, opts...)
}

func AssertTokensBelow(t testing.TB, calls []Call, maxTotal int) {
	t.Helper()
	total := 0
	known := false
	for _, call := range calls {
		usage := call.Normalized.Usage
		if usage == nil {
			continue
		}
		if usage.TotalTokens != nil {
			total += *usage.TotalTokens
			known = true
			continue
		}
		if usage.InputTokens != nil {
			total += *usage.InputTokens
			known = true
		}
		if usage.OutputTokens != nil {
			total += *usage.OutputTokens
			known = true
		}
	}
	if !known {
		t.Fatalf("no token usage was reported")
	}
	if total >= maxTotal {
		t.Fatalf("token total too high: got=%d want<%d", total, maxTotal)
	}
}

func TokensBelow(t testing.TB, calls []Call, maxTotal int) {
	t.Helper()
	AssertTokensBelow(t, calls, maxTotal)
}

func jsonMatches(t testing.TB, raw json.RawMessage, want any) bool {
	t.Helper()
	if want == nil {
		return true
	}
	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal wanted args: %v", err)
	}
	var gotValue any
	if len(raw) == 0 {
		gotValue = map[string]any{}
	} else if err := json.Unmarshal(raw, &gotValue); err != nil {
		return false
	}
	var wantValue any
	if err := json.Unmarshal(wantBytes, &wantValue); err != nil {
		t.Fatalf("unmarshal wanted args: %v", err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

func mustJSON(v any) string {
	if v == nil {
		return "<any>"
	}
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSpace(b.String())
}
