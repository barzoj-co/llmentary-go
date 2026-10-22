// SPDX-License-Identifier: MIT

package llmentary

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	// ClientAborted is a top-level Call field (DOG-27). Older proxies
	// only set normalized.client_aborted; UnmarshalJSON copies that alias.
	ClientAborted bool `json:"client_aborted,omitempty"`
	// AbortPhase is before_headers or after_headers when ClientAborted.
	// Omitted when the call is not aborted. A client close after [DONE]
	// or a terminal Responses event is not an abort.
	AbortPhase string     `json:"abort_phase,omitempty"`
	Normalized Normalized `json:"normalized"`
}

const (
	AbortPhaseBeforeHeaders = "before_headers"
	AbortPhaseAfterHeaders  = "after_headers"
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
	Mode                  string `json:"mode,omitempty"`
	Profile               string `json:"profile,omitempty"`
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
// keywords such as enumDescriptions).
type IgnoredField struct {
	Side     string `json:"side,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Path     string `json:"path,omitempty"`
	Pointer  string `json:"pointer,omitempty"`
	Severity string `json:"severity,omitempty"`
	Keyword  string `json:"keyword,omitempty"`
	Message  string `json:"message,omitempty"`
}

// SessionInfo is one row from GET /sessions (no call bodies).
type SessionInfo struct {
	ID             string     `json:"id"`
	Calls          int        `json:"calls"`
	Active         int        `json:"active"`
	Sources        []string   `json:"sources,omitempty"`
	FirstStartedAt *time.Time `json:"first_started_at,omitempty"`
	LastEndedAt    *time.Time `json:"last_ended_at,omitempty"`
}

// ProxyConfig is the GET /config payload. Version and upstreams are the
// fields the client relies on; extra keys are ignored.
type ProxyConfig struct {
	Version   string
	Upstreams map[string]string
}

func (c *ProxyConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["version"]; ok {
		var version string
		if err := json.Unmarshal(v, &version); err != nil {
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
	return nil
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
