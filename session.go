// SPDX-License-Identifier: MIT

package llmentary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	defaultProxyURL   = "http://127.0.0.1:7777"
	defaultControlURL = "http://127.0.0.1:7778"
)

// Session is a per-test llmentary helper bound to testing.TB.
// Create one with New:
//
//	lm := llmentary.New(t)
type Session struct {
	t          testing.TB
	client     *Client
	sessionID  string
	proxyURL   string
	quiet      time.Duration
	timeout    time.Duration
	httpClient *http.Client
}

// Option configures New.
type Option func(*sessionConfig)

type sessionConfig struct {
	controlURL   string
	proxyURL     string
	controlToken string
	httpClient   *http.Client
	quiet        time.Duration
	timeout      time.Duration
	sessionID    string
}

// New starts a unique recording session for t and deletes it in t.Cleanup.
// Wait defaults are 30s / 500ms, overridable with LLMENTARY_WAIT_TIMEOUT,
// LLMENTARY_WAIT_QUIET, or WithWait.
func New(t testing.TB, opts ...Option) *Session {
	t.Helper()
	cfg := sessionConfig{
		controlURL: getenvDefault("LLMENTARY_CONTROL_URL", defaultControlURL),
		proxyURL:   getenvDefault("LLMENTARY_PROXY_URL", defaultProxyURL),
		quiet:      DefaultWaitQuiet,
		timeout:    DefaultWaitTimeout,
	}
	if err := applyWaitEnv(&cfg.quiet, &cfg.timeout); err != nil {
		t.Fatalf("llmentary: %v", err)
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	clientOpts := []ClientOption{}
	if cfg.httpClient != nil {
		clientOpts = append(clientOpts, WithHTTPClient(cfg.httpClient))
	}
	if cfg.controlToken != "" {
		clientOpts = append(clientOpts, WithControlToken(cfg.controlToken))
	}
	client, err := NewClient(cfg.controlURL, clientOpts...)
	if err != nil {
		t.Fatalf("llmentary: create client: %v", err)
	}
	sessionID := strings.TrimSpace(cfg.sessionID)
	if sessionID == "" {
		sessionID = client.NewSession()
	}
	lm := &Session{
		t:          t,
		client:     client,
		sessionID:  sessionID,
		proxyURL:   strings.TrimRight(cfg.proxyURL, "/"),
		quiet:      cfg.quiet,
		timeout:    cfg.timeout,
		httpClient: cfg.httpClient,
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = lm.client.Delete(ctx, lm.sessionID)
	})
	return lm
}

// WithControlURL sets the proxy control API URL (default LLMENTARY_CONTROL_URL or http://127.0.0.1:7778).
func WithControlURL(rawURL string) Option {
	return func(c *sessionConfig) { c.controlURL = rawURL }
}

// WithProxyURL sets the proxy listen URL (default LLMENTARY_PROXY_URL or http://127.0.0.1:7777).
func WithProxyURL(rawURL string) Option {
	return func(c *sessionConfig) { c.proxyURL = rawURL }
}

// WithSessionControlToken sets the control API bearer token for this helper.
// Named distinctly from Client's WithControlToken.
func WithSessionControlToken(token string) Option {
	return func(c *sessionConfig) { c.controlToken = token }
}

// WithSessionHTTPClient sets the HTTP client used for control API calls and as the base for HTTPClient.
// Named distinctly from Client's WithHTTPClient.
func WithSessionHTTPClient(client *http.Client) Option {
	return func(c *sessionConfig) { c.httpClient = client }
}

// WithWait sets the quiet period and timeout used by Wait, Expect, and Check.
// It overrides LLMENTARY_WAIT_QUIET / LLMENTARY_WAIT_TIMEOUT for this session.
func WithWait(quiet, timeout time.Duration) Option {
	return func(c *sessionConfig) {
		c.quiet = quiet
		c.timeout = timeout
	}
}

// WithSessionID binds the helper to an existing id instead of minting one.
// Use it when OPENAI_BASE_URL must be set to PathBaseURL before New(t).
func WithSessionID(id string) Option {
	return func(c *sessionConfig) { c.sessionID = id }
}

func (lm *Session) SessionID() string { return lm.sessionID }

func (lm *Session) Header() (string, string) { return Header(lm.sessionID) }

func (lm *Session) Headers() http.Header {
	h := http.Header{}
	key, value := lm.Header()
	h.Set(key, value)
	return h
}

func (lm *Session) Baggage() string {
	return "llmentary.session.id=" + url.QueryEscape(lm.sessionID)
}

func (lm *Session) BaseURL(upstream string) string {
	if upstream == "" {
		return lm.proxyURL
	}
	return lm.proxyURL + "/u/" + strings.Trim(upstream, "/")
}

func (lm *Session) OpenAIBaseURL(upstream string) string {
	if strings.TrimSpace(upstream) == "" {
		return lm.proxyURL + "/v1"
	}
	return lm.BaseURL(upstream)
}

// PathBaseURL is the /s/{session}/… listen URL so a service that cannot send
// headers still lands on this session. Empty upstream is {proxy}/s/{id}/v1;
// a named upstream is {proxy}/s/{id}/u/{name}.
func (lm *Session) PathBaseURL(upstream string) string {
	return pathBaseURL(lm.proxyURL, lm.sessionID, upstream)
}

func pathBaseURL(proxyURL, sessionID, upstream string) string {
	base := strings.TrimRight(proxyURL, "/") + "/s/" + url.PathEscape(sessionID)
	if strings.TrimSpace(upstream) == "" {
		return base + "/v1"
	}
	return base + "/u/" + strings.Trim(upstream, "/")
}

// ListSessions is GET /sessions for this helper's control client.
func (lm *Session) ListSessions(ctx context.Context, opts ...ListSessionsOption) []SessionInfo {
	lm.t.Helper()
	list, err := lm.client.ListSessions(ctx, opts...)
	if err != nil {
		lm.t.Fatalf("llmentary: list sessions: %v", err)
	}
	return list
}

// Version is the proxy version from GET /config.
func (lm *Session) Version(ctx context.Context) string {
	lm.t.Helper()
	version, err := lm.client.Version(ctx)
	if err != nil {
		lm.t.Fatalf("llmentary: proxy version: %v", err)
	}
	return version
}

func (lm *Session) HTTPClient() *http.Client {
	client := http.Client{}
	if lm.httpClient != nil {
		client = *lm.httpClient
	}
	transport := http.DefaultTransport
	if client.Transport != nil {
		transport = client.Transport
	}
	client.Transport = sessionTransport{sessionID: lm.sessionID, base: transport}
	return &client
}

type sessionTransport struct {
	sessionID string
	base      http.RoundTripper
}

func (t sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set(SessionHeaderName, t.sessionID)
	return t.base.RoundTrip(clone)
}

func (lm *Session) Wait(ctx context.Context, opts ...WaitOption) Trace {
	lm.t.Helper()
	tr, err := lm.waitTrace(ctx, opts...)
	if err != nil {
		lm.fatalControl("wait", err)
	}
	return tr
}

func (lm *Session) waitTrace(ctx context.Context, opts ...WaitOption) (Trace, error) {
	calls, err := lm.client.Wait(ctx, lm.sessionID, lm.quiet, lm.timeout, opts...)
	if err != nil {
		return Trace{SessionID: lm.sessionID}, err
	}
	return Trace{SessionID: lm.sessionID, Calls: calls}, nil
}

func (lm *Session) Calls(ctx context.Context) Trace {
	lm.t.Helper()
	calls, err := lm.client.Calls(ctx, lm.sessionID)
	if err != nil {
		lm.fatalControl("fetch", err)
	}
	return Trace{SessionID: lm.sessionID, Calls: calls}
}

func (lm *Session) Expect(ctx context.Context, opts ...WaitOption) *Assertions {
	lm.t.Helper()
	return lm.assertions(ctx, true, opts...)
}

func (lm *Session) Check(ctx context.Context, opts ...WaitOption) *Assertions {
	lm.t.Helper()
	return lm.assertions(ctx, false, opts...)
}

// ExpectNoCalls waits with min_calls=0. A 404 session_not_found passes.
// A session that recorded calls fails with the trace summary.
func (lm *Session) ExpectNoCalls(ctx context.Context, opts ...WaitOption) bool {
	lm.t.Helper()
	return lm.noCalls(ctx, true, opts...)
}

// CheckNoCalls is ExpectNoCalls with t.Error instead of t.Fatal.
func (lm *Session) CheckNoCalls(ctx context.Context, opts ...WaitOption) bool {
	lm.t.Helper()
	return lm.noCalls(ctx, false, opts...)
}

func (lm *Session) noCalls(ctx context.Context, fatal bool, opts ...WaitOption) bool {
	lm.t.Helper()
	tr, err := lm.waitTrace(ctx, opts...)
	if err != nil {
		if IsSessionNotFound(err) {
			return true
		}
		lm.fatalControl("expect_no_calls", err)
		return false
	}
	if len(tr.Calls) == 0 {
		return true
	}
	msg := fmt.Sprintf("expected 0 LLM calls, got %d", len(tr.Calls))
	if fatal {
		lm.t.Fatalf("%s\n\nTrace summary:\n%s", msg, tr.Summary())
		return false
	}
	lm.t.Error(msg + "\n\nTrace summary:\n" + tr.Summary())
	return false
}

func (lm *Session) assertions(ctx context.Context, fatal bool, opts ...WaitOption) *Assertions {
	lm.t.Helper()
	wo := applyWaitOptions(opts)
	full, err := lm.waitTrace(ctx, opts...)
	if err != nil {
		if wo.AllowEmpty && IsSessionNotFound(err) {
			return &Assertions{t: lm.t, trace: full, sessionTrace: full, fatal: fatal, allowEmpty: true, client: lm.client, ctx: ctx}
		}
		lm.fatalControl("wait", err)
	}
	if wo.Since != nil {
		q := full.Query().Since(*wo.Since)
		wo.Since = nil
		a := q.newAssertions(lm.t, fatal, wo)
		a.client = lm.client
		a.ctx = ctx
		return a
	}
	a := &Assertions{t: lm.t, trace: full, sessionTrace: full, fatal: fatal, allowEmpty: wo.AllowEmpty, client: lm.client, ctx: ctx}
	if len(full.Calls) == 0 && !wo.AllowEmpty {
		a.failNoCalls(lm.knownIDs(ctx))
	}
	return a
}

// Mark snapshots the highest recorded seq so later Since(m) can scope a turn.
func (lm *Session) Mark(ctx context.Context) Mark {
	lm.t.Helper()
	calls, err := lm.client.Calls(ctx, lm.sessionID)
	if err != nil {
		if IsSessionNotFound(err) {
			return Mark{Seq: 0}
		}
		lm.fatalControl("mark", err)
	}
	maxSeq := 0
	for _, call := range calls {
		if call.Seq > maxSeq {
			maxSeq = call.Seq
		}
	}
	return Mark{Seq: maxSeq}
}

func (lm *Session) fatalControl(op string, err error) {
	lm.t.Helper()
	if IsSessionNotFound(err) {
		lm.t.Fatalf("llmentary: %s session %s: %v\n\nTrace summary:\n%s", op, lm.sessionID, err, Trace{SessionID: lm.sessionID}.Summary())
	}
	lm.t.Fatalf("llmentary: %s session %s: %v", op, lm.sessionID, err)
}

func (lm *Session) knownIDs(ctx context.Context) []string {
	ids, err := lm.client.knownSessionIDs(ctx)
	if err != nil {
		return nil
	}
	return ids
}

type Trace struct {
	SessionID string
	Calls     []Call
}

func (tr Trace) Query() Query { return Query{trace: tr, calls: append([]Call(nil), tr.Calls...)} }

func (tr Trace) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "session %s: %d call(s)\n", tr.SessionID, len(tr.Calls))
	for _, call := range tr.Calls {
		fmt.Fprintf(&b, "  #%d (%s) %s %s status=%d", call.Seq, call.ID, call.Provider, call.APIFamily, call.HTTPStatus)
		if finish := strings.TrimSpace(call.Normalized.FinishReason); finish != "" && !strings.EqualFold(finish, "unknown") {
			fmt.Fprintf(&b, " finish=%s", finish)
		}
		fmt.Fprintf(&b, " model=%s/%s latency=%dms retry=",
			call.Normalized.Model.Requested, call.Normalized.Model.Returned, call.LatencyMS)
		if call.RetryCount == nil {
			b.WriteString("unknown")
		} else {
			fmt.Fprintf(&b, "%d", *call.RetryCount)
		}
		fmt.Fprintf(&b, " spec=%s", call.specSummary())
		if src := strings.TrimSpace(call.SessionSource); src != "" {
			fmt.Fprintf(&b, " src=%s", src)
		}
		if tokens := call.tokenPair(); tokens != "" {
			fmt.Fprintf(&b, " tokens=%s", tokens)
		}
		b.WriteByte('\n')
		for _, tool := range call.Normalized.ToolCalls {
			fmt.Fprintf(&b, "    tool[%d] %s args=%s\n", tool.Index, tool.Name, compactJSON(tool.ArgumentsRaw))
		}
		if call.Normalized.Error != nil {
			fmt.Fprintf(&b, "    error type=%s code=%s message=%s mid_stream=%v\n", call.Normalized.Error.Type, call.Normalized.Error.Code, call.Normalized.Error.Message, call.Normalized.Error.MidStream)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c Call) tokenPair() string {
	usage := c.Normalized.Usage
	if usage == nil || (usage.InputTokens == nil && usage.OutputTokens == nil) {
		return ""
	}
	in, out := "-", "-"
	if usage.InputTokens != nil {
		in = strconv.Itoa(*usage.InputTokens)
	}
	if usage.OutputTokens != nil {
		out = strconv.Itoa(*usage.OutputTokens)
	}
	return in + "/" + out
}

type Query struct {
	trace      Trace
	calls      []Call
	filters    []string
	allowEmpty bool
}

func (q Query) Calls() []Call { return append([]Call(nil), q.calls...) }
func (q Query) Len() int      { return len(q.calls) }
func (q Query) Trace() Trace  { return Trace{SessionID: q.trace.SessionID, Calls: q.Calls()} }

// AllowEmpty lets NoErrors and ToolNotCalled pass when this query matches zero calls.
func (q Query) AllowEmpty() Query {
	q.allowEmpty = true
	return q
}

func (q Query) Expect(t testing.TB, opts ...WaitOption) *Assertions {
	t.Helper()
	return q.newAssertions(t, true, applyWaitOptions(opts))
}

func (q Query) Check(t testing.TB, opts ...WaitOption) *Assertions {
	t.Helper()
	return q.newAssertions(t, false, applyWaitOptions(opts))
}

func (q Query) newAssertions(t testing.TB, fatal bool, wo WaitOptions) *Assertions {
	filters := append([]string(nil), q.filters...)
	calls := q.Calls()
	if wo.Since != nil {
		q = q.Since(*wo.Since)
		filters = append([]string(nil), q.filters...)
		calls = q.Calls()
	}
	return &Assertions{
		t:            t,
		trace:        Trace{SessionID: q.trace.SessionID, Calls: calls},
		sessionTrace: q.trace,
		filters:      filters,
		allowEmpty:   q.allowEmpty || wo.AllowEmpty,
		fatal:        fatal,
	}
}

func (q Query) Upstream(name string) Query {
	return q.keep(fmt.Sprintf("upstream(%q)", name), func(c Call) bool { return c.Provider == name })
}

func (q Query) Model(model string) Query {
	return q.keep(fmt.Sprintf("model(%q)", model), func(c Call) bool {
		return c.Normalized.Model.Requested == model || c.Normalized.Model.Returned == model
	})
}

func (q Query) APIType(apiType string) Query {
	return q.keep(fmt.Sprintf("api_type(%q)", apiType), func(c Call) bool {
		return c.APIFamily == apiType || strings.TrimPrefix(c.APIFamily, "openai.") == apiType
	})
}

func (q Query) Status(status int) Query {
	return q.keep(fmt.Sprintf("status(%d)", status), func(c Call) bool { return c.HTTPStatus == status })
}

func (q Query) Errors() Query {
	return q.keep("errors", func(c Call) bool { return c.Normalized.Error != nil })
}

func (q Query) Retries() Query {
	return q.keep("retries", func(c Call) bool { return c.RetryCount != nil && *c.RetryCount > 0 })
}

func (q Query) Index(i int) Query {
	label := fmt.Sprintf("index(%d)", i)
	if i < 0 || i >= len(q.calls) {
		return Query{trace: q.trace, filters: appendFilter(q.filters, label), allowEmpty: q.allowEmpty}
	}
	return Query{trace: q.trace, calls: []Call{q.calls[i]}, filters: appendFilter(q.filters, label), allowEmpty: q.allowEmpty}
}

// OffersTool keeps calls that declared tool name in tools_declared.
func (q Query) OffersTool(name string) Query {
	return q.keep(fmt.Sprintf("offers_tool(%q)", name), func(c Call) bool {
		for _, tool := range c.Normalized.ToolsDeclared {
			if tool.Name == name {
				return true
			}
		}
		return false
	})
}

// WithoutTools keeps calls that offered no tools (router / structured-output).
func (q Query) WithoutTools() Query {
	return q.keep("without_tools", func(c Call) bool { return len(c.Normalized.ToolsDeclared) == 0 })
}

// Since keeps calls with seq greater than mark.Seq.
func (q Query) Since(m Mark) Query {
	return q.keep(fmt.Sprintf("since=%d", m.Seq), func(c Call) bool { return c.Seq > m.Seq })
}

func (q Query) keep(label string, fn func(Call) bool) Query {
	out := make([]Call, 0, len(q.calls))
	for _, call := range q.calls {
		if fn(call) {
			out = append(out, call)
		}
	}
	return Query{trace: q.trace, calls: out, filters: appendFilter(q.filters, label), allowEmpty: q.allowEmpty}
}

func appendFilter(filters []string, label string) []string {
	return append(append([]string(nil), filters...), label)
}

type Assertions struct {
	t             testing.TB
	trace         Trace
	sessionTrace  Trace
	filters       []string
	allowEmpty    bool
	fatal         bool
	summaryQueued bool
	client        *Client
	ctx           context.Context
}

func (a *Assertions) Trace() Trace {
	return a.trace
}

func (a *Assertions) fail(format string, args ...any) bool {
	a.t.Helper()
	msg := fmt.Sprintf(format, args...)
	if a.fatal {
		a.t.Fatal(msg + "\n\nTrace summary:\n" + a.summaryText())
		return false
	}
	a.t.Error(msg)
	a.queueSoftSummary()
	return false
}

func (a *Assertions) summaryText() string {
	if a.sessionTrace.SessionID != "" || len(a.sessionTrace.Calls) > 0 {
		return a.sessionTrace.Summary()
	}
	return a.trace.Summary()
}

func (a *Assertions) queueSoftSummary() {
	if a.summaryQueued {
		return
	}
	a.summaryQueued = true
	a.t.Cleanup(func() {
		a.t.Helper()
		a.t.Error("Trace summary:\n" + a.summaryText())
	})
}

func (a *Assertions) requireMatchedCalls() bool {
	a.t.Helper()
	if a.allowEmpty || len(a.trace.Calls) > 0 || len(a.filters) == 0 {
		return true
	}
	return a.fail("no calls matched query (%s); session has %d calls", strings.Join(a.filters, ", "), len(a.sessionTrace.Calls))
}

func (a *Assertions) failNoCalls(known []string) bool {
	a.t.Helper()
	return a.fail("%s", newSessionNotFound(a.trace.SessionID, known, nil).Error())
}

func (a *Assertions) CallCount(want int, opts ...CallCountOption) bool {
	a.t.Helper()
	var options CallCountOptions
	for _, opt := range opts {
		opt(&options)
	}
	got := 0
	for _, call := range a.trace.Calls {
		if options.ExcludeRetries && call.RetryCount != nil && *call.RetryCount > 0 {
			continue
		}
		got++
	}
	if got != want {
		return a.fail("LLM call count mismatch: got %d, want %d", got, want)
	}
	return true
}

func (a *Assertions) Retries(want int) bool {
	a.t.Helper()
	got := 0
	for _, call := range a.trace.Calls {
		if call.RetryCount != nil && *call.RetryCount > 0 {
			got++
		}
	}
	if got != want {
		return a.fail("retry call count mismatch: got %d, want %d", got, want)
	}
	return true
}

func (a *Assertions) NoRetries() bool {
	a.t.Helper()
	return a.Retries(0)
}

func (a *Assertions) ToolCalled(name string, opts ...ToolAssertionOption) bool {
	a.t.Helper()
	matches := matchingTools(a.trace.Calls, name, opts...)
	if len(matches) == 0 {
		return a.fail("expected tool %q to be called", name)
	}
	return true
}

func (a *Assertions) ToolNotCalled(name string) bool {
	a.t.Helper()
	if !a.requireMatchedCalls() {
		return false
	}
	if len(matchingTools(a.trace.Calls, name)) > 0 {
		return a.fail("expected tool %q not to be called", name)
	}
	return true
}

func (a *Assertions) ToolCalledTimes(name string, n int, opts ...ToolAssertionOption) bool {
	a.t.Helper()
	got := len(matchingTools(a.trace.Calls, name, opts...))
	if got != n {
		return a.fail("tool %q call count mismatch: got %d, want %d", name, got, n)
	}
	return true
}

func (a *Assertions) ToolOrder(names ...string) bool {
	a.t.Helper()
	got := toolNames(a.trace.Calls)
	if !equalStrings(got, names) {
		return a.fail("tool order mismatch: got %v, want %v", got, names)
	}
	return true
}

func (a *Assertions) ToolBefore(before, after string) bool {
	a.t.Helper()
	positions := map[string]int{}
	for i, name := range toolNames(a.trace.Calls) {
		if _, ok := positions[name]; !ok {
			positions[name] = i
		}
	}
	b, bok := positions[before]
	af, aok := positions[after]
	if !bok || !aok || b >= af {
		return a.fail("expected tool %q before %q, got order %v", before, after, toolNames(a.trace.Calls))
	}
	return true
}

func (a *Assertions) ParallelToolCalls(names ...string) bool {
	a.t.Helper()
	want := append([]string(nil), names...)
	sort.Strings(want)
	for _, call := range a.trace.Calls {
		if len(call.Normalized.ToolCalls) < len(want) {
			continue
		}
		got := make([]string, 0, len(call.Normalized.ToolCalls))
		for _, tool := range call.Normalized.ToolCalls {
			got = append(got, tool.Name)
		}
		sort.Strings(got)
		if containsAllStrings(got, want) {
			return true
		}
	}
	return a.fail("expected parallel tool calls %v in a single LLM call", names)
}

func (a *Assertions) ToolResultFedBack(callID string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		for _, result := range call.Normalized.ToolResultsIn {
			if result.CallID == callID {
				return true
			}
		}
	}
	return a.fail("expected tool result for call id %q to be fed back", callID)
}

func (a *Assertions) Model(model string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.Model.Requested == model || call.Normalized.Model.Returned == model {
			return true
		}
	}
	return a.fail("expected model %q", model)
}

func (a *Assertions) RequestParam(name string, want any) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		var body map[string]any
		if json.Unmarshal(call.RawRequest, &body) == nil && jsonDeepEqual(body[name], want) {
			return true
		}
	}
	return a.fail("expected request param %s=%s", name, mustJSON(want))
}

func (a *Assertions) FinishReason(reason string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.FinishReason == reason || call.Normalized.NativeFinishReason == reason {
			return true
		}
	}
	return a.fail("expected finish reason %q", reason)
}

func (a *Assertions) TokensBelow(maxTotal int) bool {
	a.t.Helper()
	return a.tokenBound(maxTotal, true)
}

func (a *Assertions) TokensAtMost(maxTotal int) bool {
	a.t.Helper()
	return a.tokenBound(maxTotal, false)
}

func (a *Assertions) tokenBound(maxTotal int, strict bool) bool {
	a.t.Helper()
	total, known := 0, false
	for _, call := range a.trace.Calls {
		if call.Normalized.Usage == nil {
			continue
		}
		if call.Normalized.Usage.TotalTokens != nil {
			total += *call.Normalized.Usage.TotalTokens
			known = true
		} else {
			if call.Normalized.Usage.InputTokens != nil {
				total += *call.Normalized.Usage.InputTokens
				known = true
			}
			if call.Normalized.Usage.OutputTokens != nil {
				total += *call.Normalized.Usage.OutputTokens
				known = true
			}
		}
	}
	if !known {
		return a.fail("no token usage reported")
	}
	if strict && total >= maxTotal {
		return a.fail("token total too high: got %d, want <%d", total, maxTotal)
	}
	if !strict && total > maxTotal {
		return a.fail("token total too high: got %d, want <=%d", total, maxTotal)
	}
	return true
}

func (a *Assertions) LatencyBelow(d time.Duration) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if time.Duration(call.LatencyMS)*time.Millisecond >= d {
			return a.fail("call #%d latency %s exceeds %s", call.Seq, time.Duration(call.LatencyMS)*time.Millisecond, d)
		}
	}
	return true
}

func (a *Assertions) TTFTBelow(d time.Duration) bool {
	a.t.Helper()
	known := false
	for _, call := range a.trace.Calls {
		if call.FirstByteAt == nil {
			continue
		}
		known = true
		ttft := call.FirstByteAt.Sub(call.StartedAt)
		if ttft >= d {
			return a.fail("call #%d TTFT %s exceeds %s", call.Seq, ttft, d)
		}
	}
	if !known {
		return a.fail("no first-byte time was reported")
	}
	return true
}

func (a *Assertions) NoErrors() bool {
	a.t.Helper()
	if !a.requireMatchedCalls() {
		return false
	}
	for _, call := range a.trace.Calls {
		if call.Normalized.Error != nil {
			return a.fail("unexpected error in call #%d: %+v", call.Seq, call.Normalized.Error)
		}
	}
	return true
}

func (a *Assertions) HasError() bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.Error != nil {
			return true
		}
	}
	return a.fail("expected at least one LLM error")
}

func (a *Assertions) MidStreamError() bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.Error != nil && call.Normalized.Error.MidStream {
			return true
		}
	}
	return a.fail("expected an error inside an HTTP-200 stream")
}

func (a *Assertions) SystemPromptContains(substr string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if strings.Contains(call.Normalized.SystemPrompt, substr) {
			return true
		}
	}
	return a.fail("expected system prompt to contain %q", substr)
}

func (a *Assertions) ResponseContains(substr string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if strings.Contains(call.Normalized.OutputText, substr) {
			return true
		}
	}
	return a.fail("expected response content to contain %q", substr)
}

func (a *Assertions) ResponseMatches(pattern string) bool {
	a.t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		return a.fail("invalid response regexp %q: %v", pattern, err)
	}
	for _, call := range a.trace.Calls {
		if re.MatchString(call.Normalized.OutputText) {
			return true
		}
	}
	return a.fail("expected response content to match %q", pattern)
}

func (a *Assertions) ResponseJSONValid() bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if json.Valid([]byte(call.Normalized.OutputText)) {
			return true
		}
	}
	return a.fail("expected at least one response to contain valid JSON")
}

func (a *Assertions) ResponseJSONSchemaValid(schema map[string]any) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		var value any
		if json.Unmarshal([]byte(call.Normalized.OutputText), &value) == nil && validateBasicSchema(value, schema) == nil {
			return true
		}
	}
	return a.fail("expected at least one response to satisfy schema %s", mustJSON(schema))
}

type ToolAssertionOption func(*toolAssertion)

type toolAssertion struct {
	matchers []func(ToolCall) error
}

func ArgsSubset(want map[string]any) ToolAssertionOption {
	return func(a *toolAssertion) {
		a.matchers = append(a.matchers, func(tc ToolCall) error {
			var got any
			if len(tc.Arguments) == 0 {
				got = map[string]any{}
			} else if err := json.Unmarshal(tc.Arguments, &got); err != nil {
				return fmt.Errorf("args are not valid JSON: %v", err)
			}
			if !jsonSubset(got, want) {
				return fmt.Errorf("args %s do not include subset %s", string(tc.Arguments), mustJSON(want))
			}
			return nil
		})
	}
}

func ArgPath(path string, want any) ToolAssertionOption {
	return func(a *toolAssertion) {
		a.matchers = append(a.matchers, func(tc ToolCall) error {
			var got any
			if err := json.Unmarshal(tc.Arguments, &got); err != nil {
				return fmt.Errorf("args are not valid JSON: %v", err)
			}
			value, ok := jsonPath(got, path)
			if !ok || !jsonDeepEqual(value, want) {
				return fmt.Errorf("arg path %q = %s, want %s", path, mustJSON(value), mustJSON(want))
			}
			return nil
		})
	}
}

func ArgsMatch(fn func(json.RawMessage) error) ToolAssertionOption {
	return func(a *toolAssertion) {
		a.matchers = append(a.matchers, func(tc ToolCall) error { return fn(tc.Arguments) })
	}
}

func matchingTools(calls []Call, name string, opts ...ToolAssertionOption) []ToolCall {
	assertion := toolAssertion{}
	for _, opt := range opts {
		opt(&assertion)
	}
	var out []ToolCall
	for _, call := range calls {
		for _, tool := range call.Normalized.ToolCalls {
			if tool.Name != name {
				continue
			}
			ok := true
			for _, matcher := range assertion.matchers {
				if matcher(tool) != nil {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, tool)
			}
		}
	}
	return out
}

func toolNames(calls []Call) []string {
	var out []string
	for _, call := range calls {
		for _, tool := range call.Normalized.ToolCalls {
			out = append(out, tool.Name)
		}
	}
	return out
}

func jsonSubset(got any, want any) bool {
	switch wantTyped := want.(type) {
	case map[string]any:
		gotMap, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, wantValue := range wantTyped {
			if !jsonSubset(gotMap[key], wantValue) {
				return false
			}
		}
		return true
	case []any:
		gotSlice, ok := got.([]any)
		if !ok || len(gotSlice) < len(wantTyped) {
			return false
		}
		for i, wantValue := range wantTyped {
			if !jsonSubset(gotSlice[i], wantValue) {
				return false
			}
		}
		return true
	default:
		return jsonDeepEqual(got, want)
	}
}

func jsonPath(value any, path string) (any, bool) {
	current := value
	for _, part := range strings.Split(path, ".") {
		if part == "" || part == "$" {
			continue
		}
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[part]
			if !ok {
				return nil, false
			}
		case []any:
			var idx int
			if _, err := fmt.Sscanf(part, "%d", &idx); err != nil || idx < 0 || idx >= len(typed) {
				return nil, false
			}
			current = typed[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func jsonDeepEqual(got, want any) bool {
	gotBytes, _ := json.Marshal(got)
	wantBytes, _ := json.Marshal(want)
	var gotJSON any
	var wantJSON any
	_ = json.Unmarshal(gotBytes, &gotJSON)
	_ = json.Unmarshal(wantBytes, &wantJSON)
	return fmt.Sprintf("%#v", gotJSON) == fmt.Sprintf("%#v", wantJSON)
}

func validateBasicSchema(value any, schema map[string]any) error {
	if typ, _ := schema["type"].(string); typ != "" {
		if !matchesJSONType(value, typ) {
			return fmt.Errorf("type mismatch: got %T want %s", value, typ)
		}
	}
	if required := requiredKeys(schema["required"]); len(required) > 0 {
		obj, _ := value.(map[string]any)
		for _, key := range required {
			if _, ok := obj[key]; !ok {
				return fmt.Errorf("missing required property %q", key)
			}
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		obj, _ := value.(map[string]any)
		for key, rawSchema := range props {
			if child, ok := obj[key]; ok {
				childSchema, _ := rawSchema.(map[string]any)
				if childSchema != nil {
					if err := validateBasicSchema(child, childSchema); err != nil {
						return fmt.Errorf("%s: %w", key, err)
					}
				}
			}
		}
	}
	return nil
}

func requiredKeys(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if key, ok := item.(string); ok {
				out = append(out, key)
			}
		}
		return out
	default:
		return nil
	}
}

func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && n == float64(int64(n))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func compactJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	var b bytes.Buffer
	if json.Compact(&b, []byte(raw)) == nil {
		return b.String()
	}
	return raw
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsAllStrings(got, want []string) bool {
	counts := map[string]int{}
	for _, value := range got {
		counts[value]++
	}
	for _, value := range want {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
