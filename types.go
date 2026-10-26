// SPDX-License-Identifier: MIT

package llmentary

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	SessionHeaderName = "X-Test-Session"
	DefaultSessionID  = "default"
)

// sessionPayload is the control-API JSON envelope for one recorded session.
// The test-facing handle is Session (see New).
type sessionPayload struct {
	ID    string `json:"id"`
	Calls []Call `json:"calls"`
}

type Call struct {
	ID            string            `json:"id"`
	SessionID     string            `json:"session_id"`
	SessionSource string            `json:"session_source,omitempty"`
	Seq           int               `json:"seq"`
	StartedAt     time.Time         `json:"started_at"`
	FirstByteAt   *time.Time        `json:"first_byte_at,omitempty"`
	EndedAt       time.Time         `json:"ended_at"`
	LatencyMS     int64             `json:"latency_ms"`
	Provider      string            `json:"provider"`
	APIFamily     string            `json:"api_family"`
	Upstream      Upstream          `json:"upstream"`
	Request       Exchange          `json:"request"`
	Response      Exchange          `json:"response"`
	RequestIDs    map[string]string `json:"request_ids,omitempty"`
	RawRequest    []byte            `json:"raw_request,omitempty"`
	RawResponse   []byte            `json:"raw_response,omitempty"`
	SSEEvents     []SSEEvent        `json:"sse_events,omitempty"`
	Stream        bool              `json:"stream"`
	HTTPStatus    int               `json:"http_status"`
	RetryCount    *int              `json:"retry_count,omitempty"`
	RetryAfter    string            `json:"retry_after,omitempty"`
	// UpstreamCalled is true when the outbound request was written
	// (httptrace.WroteRequest). A header timeout or cancel can leave
	// this false.
	UpstreamCalled bool `json:"upstream_called,omitempty"`
	// ClientAborted is a top-level Call field (DOG-27). Older proxies
	// only set normalized.client_aborted; UnmarshalJSON copies that alias.
	ClientAborted bool `json:"client_aborted,omitempty"`
	// AbortPhase is before_headers or after_headers when ClientAborted.
	// Omitted when the call is not aborted. A client close after [DONE]
	// or a terminal Responses event is not an abort.
	AbortPhase string `json:"abort_phase,omitempty"`
	// RecordedOutcome is the terminal state committed by the proxy
	// (DOG-58). Absent on older proxies; Outcome() derives it then.
	RecordedOutcome string     `json:"outcome,omitempty"`
	Normalized      Normalized `json:"normalized"`
}

const (
	AbortPhaseBeforeHeaders = "before_headers"
	AbortPhaseAfterHeaders  = "after_headers"

	// Spec targets on GET /config spec_targets and normalized.validation.target.
	SpecTargetOpenAI           = "openai"
	SpecTargetOpenAICompatible = "openai-compatible"

	// CallError.Normalized values for optional upstream timeouts (DOG-39).
	ErrorNormalizedUpstreamTimeout     = "upstream_timeout"
	ErrorNormalizedUpstreamIdleTimeout = "upstream_idle_timeout"
	ErrorSourceProxy                   = "proxy"

	// OutcomeCommitted, OutcomeCancelled and OutcomeFailed are the terminal
	// states of a settled call (DOG-58).
	OutcomeCommitted = "committed"
	OutcomeCancelled = "cancelled"
	OutcomeFailed    = "failed"
)

func (c *Call) UnmarshalJSON(data []byte) error {
	type callAlias Call
	var raw callAlias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = Call(raw)
	if !knownAbortPhase(c.AbortPhase) {
		c.AbortPhase = ""
	}
	if !c.ClientAborted && c.Normalized.ClientAborted {
		c.ClientAborted = true
	}
	if !c.ClientAborted {
		c.AbortPhase = ""
		return nil
	}
	if c.AbortPhase == "" {
		c.AbortPhase = abortPhaseFromNormalized(c.Normalized)
	}
	return nil
}

func knownAbortPhase(phase string) bool {
	switch phase {
	case "", AbortPhaseBeforeHeaders, AbortPhaseAfterHeaders:
		return true
	default:
		return false
	}
}

func abortPhaseFromNormalized(n Normalized) string {
	if n.Raw == nil {
		return ""
	}
	var extra struct {
		AbortPhase string `json:"abort_phase"`
		Phase      string `json:"phase"`
	}
	if json.Unmarshal(n.Raw, &extra) != nil {
		return ""
	}
	switch extra.AbortPhase {
	case AbortPhaseBeforeHeaders, AbortPhaseAfterHeaders:
		return extra.AbortPhase
	}
	switch extra.Phase {
	case AbortPhaseBeforeHeaders, AbortPhaseAfterHeaders:
		return extra.Phase
	}
	return ""
}

// Aborted reports a client disconnect. It prefers the typed field and
// falls back to the deprecated normalized.client_aborted alias.
func (c Call) Aborted() bool {
	return c.ClientAborted || c.Normalized.ClientAborted
}

// Outcome reports the terminal state of a settled call: OutcomeCommitted,
// OutcomeCancelled or OutcomeFailed. The proxy-recorded outcome is returned
// when it is one of those three values; an absent or unknown recorded value
// is derived from the error, http_status, client abort and stream_truncated
// fields, so older proxies work too. It never returns "" for a settled call,
// and the abort phase (before_headers or after_headers) never changes the
// outcome. In-flight calls are reported separately as InFlightCall.
func (c Call) Outcome() string {
	switch c.RecordedOutcome {
	case OutcomeCommitted, OutcomeCancelled, OutcomeFailed:
		return c.RecordedOutcome
	}
	err := c.Normalized.Error
	if err != nil && !clientAbortedError(c, err) {
		return OutcomeFailed
	}
	if c.HTTPStatus >= 400 {
		return OutcomeFailed
	}
	if c.Aborted() {
		return OutcomeCancelled
	}
	if c.Normalized.StreamTruncated {
		return OutcomeFailed
	}
	if c.HTTPStatus == 0 {
		return OutcomeFailed
	}
	return OutcomeCommitted
}

func clientAbortedError(c Call, err *CallError) bool {
	if err.Normalized != "client_aborted" || !c.Aborted() {
		return false
	}
	return err.Source == "" || err.Source == "client"
}

type Upstream struct {
	Method     string `json:"method"`
	URL        string `json:"url"`
	APIVersion string `json:"api_version,omitempty"`
	Deployment string `json:"deployment,omitempty"`
}

// UpstreamURL is the recorded upstream URL, or "" when absent.
func (c Call) UpstreamURL() string {
	return c.Upstream.URL
}

type Exchange struct {
	Headers map[string][]string `json:"headers,omitempty"`
}

type SSEEvent struct {
	Event   string `json:"event,omitempty"`
	Data    []byte `json:"data,omitempty"`
	Comment string `json:"comment,omitempty"`
	AtMS    int64  `json:"at_ms"`
}

type Normalized struct {
	DerivedVersion     int             `json:"derived_version"`
	Model              Model           `json:"model"`
	ServedBy           string          `json:"served_by,omitempty"`
	SystemPromptHash   string          `json:"system_prompt_hash,omitempty"`
	SystemPrompt       string          `json:"system_prompt,omitempty"`
	MessagesCount      int             `json:"messages_count,omitempty"`
	PromptChars        int             `json:"prompt_chars,omitempty"`
	ContextServerSide  bool            `json:"context_server_side,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	ToolsDeclared      []ToolDecl      `json:"tools_declared,omitempty"`
	ToolCalls          []ToolCall      `json:"tool_calls,omitempty"`
	ToolResultsIn      []ToolResult    `json:"tool_results_in,omitempty"`
	OutputText         string          `json:"output_text,omitempty"`
	Reasoning          *Reasoning      `json:"reasoning,omitempty"`
	FinishReason       string          `json:"finish_reason,omitempty"`
	NativeFinishReason string          `json:"native_finish_reason,omitempty"`
	Usage              *Usage          `json:"usage,omitempty"`
	ContentFilter      *ContentFilter  `json:"content_filter,omitempty"`
	Error              *CallError      `json:"error,omitempty"`
	StreamTruncated    bool            `json:"stream_truncated,omitempty"`
	ClientAborted      bool            `json:"client_aborted,omitempty"`
	ParserWarnings     []string        `json:"parser_warnings,omitempty"`
	Raw                json.RawMessage `json:"raw,omitempty"`
	// Validation is per-call spec-check metadata. It is not a finding list.
	// Empty spec_violations with validation present is clean.
	Validation *Validation `json:"validation,omitempty"`
	// SpecViolations is a pointer so JSON absent (not checked) differs from [].
	SpecViolations *[]SpecViolation `json:"spec_violations,omitempty"`
	IgnoredFields  *[]IgnoredField  `json:"ignored_fields,omitempty"`
}

// Validation is the spec-check metadata block on normalized.validation.
// It says whether the call was checked and how, not what was found.
type Validation struct {
	Mode    string `json:"mode,omitempty"`
	Profile string `json:"profile,omitempty"`
	// Target is the per-upstream spec target: openai or openai-compatible.
	// Absent on older proxies and when validation was not run.
	Target                string `json:"target,omitempty"`
	SpecSHA               string `json:"spec_sha,omitempty"`
	SchemaSubsetRetrieved string `json:"schema_subset_retrieved,omitempty"`
	RequestChecked        bool   `json:"request_checked,omitempty"`
	ResponseChecked       bool   `json:"response_checked,omitempty"`
	SkippedReason         string `json:"skipped_reason,omitempty"`
	DurationUS            int64  `json:"duration_us,omitempty"`
	Truncated             bool   `json:"truncated,omitempty"`
}

// SpecViolation is one schema/spec finding from normalized.spec_violations.
type SpecViolation struct {
	Side       string `json:"side,omitempty"`
	Source     string `json:"source,omitempty"`
	Path       string `json:"path,omitempty"`
	Pointer    string `json:"pointer,omitempty"`
	Keyword    string `json:"keyword,omitempty"`
	Message    string `json:"message,omitempty"`
	Profile    string `json:"profile,omitempty"`
	SchemaRef  string `json:"schema_ref,omitempty"`
	Offset     *int   `json:"offset,omitempty"`
	EventIndex *int   `json:"event_index,omitempty"`
}

// IgnoredField is one normalized.ignored_fields entry (for example vendor
// keywords such as enumDescriptions). Proxy v0.5.0 writes context
// (request|response), not side. FilterSide prefers context, then side.
type IgnoredField struct {
	Side     string `json:"side,omitempty"`
	Context  string `json:"context,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Path     string `json:"path,omitempty"`
	Pointer  string `json:"pointer,omitempty"`
	Severity string `json:"severity,omitempty"`
	Keyword  string `json:"keyword,omitempty"`
	Message  string `json:"message,omitempty"`
}

// FilterSide is the request/response label used by Side("…") filters.
// It prefers context (what the proxy emits) and falls back to side.
func (f IgnoredField) FilterSide() string {
	if c := strings.TrimSpace(f.Context); c != "" {
		return c
	}
	return strings.TrimSpace(f.Side)
}

// SessionSpecCounts is GET /sessions[].spec (DOG-51).
type SessionSpecCounts struct {
	Checked     int `json:"checked"`
	Clean       int `json:"clean"`
	Findings    int `json:"findings"`
	RequestOnly int `json:"request_only"`
	NotChecked  int `json:"not_checked"`
}

func (s *SessionSpecCounts) UnmarshalJSON(data []byte) error {
	var raw struct {
		Checked      *int `json:"checked"`
		Clean        *int `json:"clean"`
		Findings     *int `json:"findings"`
		RequestOnly  *int `json:"request_only"`
		NotChecked   *int `json:"not_checked"`
		CheckedA     *int `json:"Checked"`
		CleanA       *int `json:"Clean"`
		FindingsA    *int `json:"Findings"`
		RequestOnlyA *int `json:"RequestOnly"`
		NotCheckedA  *int `json:"NotChecked"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Checked = firstPresentInt(raw.Checked, raw.CheckedA)
	s.Clean = firstPresentInt(raw.Clean, raw.CleanA)
	s.Findings = firstPresentInt(raw.Findings, raw.FindingsA)
	s.RequestOnly = firstPresentInt(raw.RequestOnly, raw.RequestOnlyA)
	s.NotChecked = firstPresentInt(raw.NotChecked, raw.NotCheckedA)
	return nil
}

func firstPresentInt(values ...*int) int {
	for _, v := range values {
		if v != nil {
			return *v
		}
	}
	return 0
}

// SessionInfo is one row from GET /sessions (no call bodies).
type SessionInfo struct {
	ID             string             `json:"id"`
	Calls          int                `json:"calls"`
	Active         int                `json:"active"`
	Sources        []string           `json:"sources,omitempty"`
	FirstStartedAt *time.Time         `json:"first_started_at,omitempty"`
	LastEndedAt    *time.Time         `json:"last_ended_at,omitempty"`
	Spec           *SessionSpecCounts `json:"spec,omitempty"`
}

// ProxyConfig is the GET /config payload. Version, upstreams, spec
// targets, and optional upstream timeouts are typed; extra keys are ignored.
type ProxyConfig struct {
	Version               string
	Upstreams             map[string]string
	SpecTargets           map[string]string
	UpstreamHeaderTimeout string
	UpstreamIdleTimeout   string
}

func (c *ProxyConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["version"]; ok {
		version, err := unmarshalConfigString(v)
		if err != nil {
			return err
		}
		c.Version = version
	}
	if u, ok := raw["upstreams"]; ok {
		upstreams, err := parseConfigUpstreams(u)
		if err != nil {
			return err
		}
		c.Upstreams = upstreams
	}
	if t, ok := raw["spec_targets"]; ok {
		targets, err := parseConfigSpecTargets(t)
		if err != nil {
			return err
		}
		c.SpecTargets = targets
	}
	if h, ok := raw["upstream_header_timeout"]; ok {
		header, err := unmarshalConfigString(h)
		if err != nil {
			return err
		}
		c.UpstreamHeaderTimeout = header
	}
	if idle, ok := raw["upstream_idle_timeout"]; ok {
		idleTimeout, err := unmarshalConfigString(idle)
		if err != nil {
			return err
		}
		c.UpstreamIdleTimeout = idleTimeout
	}
	return nil
}

// SpecTarget returns the effective spec target for an upstream name, or
// "" when GET /config omitted that name.
func (c ProxyConfig) SpecTarget(upstream string) string {
	if c.SpecTargets == nil {
		return ""
	}
	return c.SpecTargets[upstream]
}

// HeaderTimeout parses upstream_header_timeout. ok is false when the
// field was omitted or empty. A present "0s" is disabled (0, true, nil).
func (c ProxyConfig) HeaderTimeout() (time.Duration, bool, error) {
	return parseConfigDuration(c.UpstreamHeaderTimeout)
}

// IdleTimeout parses upstream_idle_timeout. Same empty-vs-zero rule as
// HeaderTimeout.
func (c ProxyConfig) IdleTimeout() (time.Duration, bool, error) {
	return parseConfigDuration(c.UpstreamIdleTimeout)
}

func parseConfigUpstreams(raw json.RawMessage) (map[string]string, error) {
	var asMap map[string]string
	if err := json.Unmarshal(raw, &asMap); err == nil && asMap != nil {
		return asMap, nil
	}
	var asAnyMap map[string]any
	if err := json.Unmarshal(raw, &asAnyMap); err == nil && asAnyMap != nil {
		out := make(map[string]string, len(asAnyMap))
		for name, value := range asAnyMap {
			switch typed := value.(type) {
			case string:
				out[name] = typed
			case map[string]any:
				if url, _ := typed["url"].(string); url != "" {
					out[name] = url
					continue
				}
				if url, _ := typed["base_url"].(string); url != "" {
					out[name] = url
				}
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	var asList []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(raw, &asList); err == nil {
		out := make(map[string]string, len(asList))
		for _, item := range asList {
			if item.Name != "" {
				out[item.Name] = item.URL
			}
		}
		return out, nil
	}
	return nil, nil
}

func parseConfigSpecTargets(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var asMap map[string]string
	if err := json.Unmarshal(raw, &asMap); err == nil {
		return asMap, nil
	}
	var asAnyMap map[string]any
	if err := json.Unmarshal(raw, &asAnyMap); err == nil && asAnyMap != nil {
		out := make(map[string]string, len(asAnyMap))
		for name, value := range asAnyMap {
			if s, ok := value.(string); ok && s != "" {
				out[name] = s
			}
		}
		if len(out) > 0 {
			return out, nil
		}
		return map[string]string{}, nil
	}
	return nil, nil
}

func unmarshalConfigString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

func parseConfigDuration(raw string) (time.Duration, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false, nil
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d < 0 {
			return 0, false, fmt.Errorf("config duration %q must be non-negative", raw)
		}
		return d, true, nil
	}
	secs, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false, fmt.Errorf("config duration %q: want a Go duration like 2s or bare seconds like 2", raw)
	}
	if secs < 0 {
		return 0, false, fmt.Errorf("config duration %q must be non-negative", raw)
	}
	return time.Duration(secs * float64(time.Second)), true, nil
}

type Model struct {
	Requested string `json:"requested,omitempty"`
	Returned  string `json:"returned,omitempty"`
}

type ToolDecl struct {
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	SchemaHash string `json:"schema_hash,omitempty"`
}

type ToolCall struct {
	Index          int             `json:"index"`
	ID             string          `json:"id,omitempty"`
	ItemID         string          `json:"item_id,omitempty"`
	Name           string          `json:"name"`
	Namespace      string          `json:"namespace,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	ArgumentsRaw   string          `json:"arguments_raw,omitempty"`
	Arguments      json.RawMessage `json:"arguments,omitempty"`
	ArgsParseError string          `json:"args_parse_error,omitempty"`
}

type ToolResult struct {
	CallID     string `json:"call_id,omitempty"`
	Name       string `json:"name,omitempty"`
	OutputHash string `json:"output_hash,omitempty"`
}

type Reasoning struct {
	Present   bool `json:"present"`
	Chars     int  `json:"chars,omitempty"`
	Encrypted bool `json:"encrypted,omitempty"`
}

type Usage struct {
	InputTokens       *int            `json:"input_tokens,omitempty"`
	OutputTokens      *int            `json:"output_tokens,omitempty"`
	TotalTokens       *int            `json:"total_tokens,omitempty"`
	CachedInputTokens *int            `json:"cached_input_tokens,omitempty"`
	CacheWriteTokens  *int            `json:"cache_write_tokens,omitempty"`
	ReasoningTokens   *int            `json:"reasoning_tokens,omitempty"`
	CostUSD           *float64        `json:"cost_usd,omitempty"`
	Source            string          `json:"source,omitempty"`
	Raw               json.RawMessage `json:"raw,omitempty"`
}

type ContentFilter struct {
	PromptFiltered     bool                    `json:"prompt_filtered,omitempty"`
	CompletionFiltered bool                    `json:"completion_filtered,omitempty"`
	Categories         map[string]FilterResult `json:"categories,omitempty"`
	FilterSystemError  bool                    `json:"filter_system_error,omitempty"`
	Raw                []json.RawMessage       `json:"raw,omitempty"`
}

type FilterResult struct {
	Filtered bool   `json:"filtered,omitempty"`
	Severity string `json:"severity,omitempty"`
	Detected bool   `json:"detected,omitempty"`
}

type CallError struct {
	HTTPStatus int    `json:"http_status,omitempty"`
	Type       string `json:"type,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	Normalized string `json:"normalized,omitempty"`
	MidStream  bool   `json:"mid_stream,omitempty"`
	Source     string `json:"source,omitempty"`
}

func NewSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}

func Header(sessionID string) (string, string) {
	return SessionHeaderName, sessionID
}
