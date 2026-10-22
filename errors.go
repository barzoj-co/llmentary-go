// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	errorTypeSessionNotFound = "session_not_found"
	errorTypeWaitTimeout     = "wait_timeout"
)

// SessionNotFoundError is returned when GET /sessions/{id} is 404
// session_not_found, when wait on a never-created session returns 404
// (min_calls=0), or when Expect/Check runs on a session that never
// recorded a call. The message always names the requested id and, when
// known, the ids from GET /sessions.
type SessionNotFoundError struct {
	SessionID string
	KnownIDs  []string
	Err       error
}

func (e *SessionNotFoundError) Error() string {
	if e == nil {
		return "no calls recorded for session"
	}
	msg := fmt.Sprintf("no calls recorded for session %s", e.SessionID)
	if hint := knownSessionsHint(e.KnownIDs); hint != "" {
		msg += "; " + hint
	}
	return msg
}

func knownSessionsHint(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "known sessions: " + strings.Join(ids, ", ")
}

func countKnownSessionsHints(s string) int {
	return strings.Count(s, "known sessions:")
}

func (e *SessionNotFoundError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsSessionNotFound reports whether err is or wraps SessionNotFoundError.
func IsSessionNotFound(err error) bool {
	var snf *SessionNotFoundError
	return errors.As(err, &snf)
}

// IsWaitTimeout reports whether err is or wraps WaitTimeoutError.
func IsWaitTimeout(err error) bool {
	var wt *WaitTimeoutError
	return errors.As(err, &wt)
}

const (
	// InFlightAwaitingHeaders is error.in_flight[].phase before upstream headers.
	InFlightAwaitingHeaders = "awaiting_headers"
	// InFlightStreaming is error.in_flight[].phase after upstream headers.
	InFlightStreaming = "streaming"
)

// InFlightCall is a still-running call from a 504 wait_timeout (or GET
// ?include=in_flight). It has no request/response bodies or headers.
type InFlightCall struct {
	CallID        string    `json:"call_id"`
	Seq           int       `json:"seq"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	APIFamily     string    `json:"api_family,omitempty"`
	Upstream      string    `json:"upstream,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	ElapsedMS     int       `json:"elapsed_ms"`
	Phase         string    `json:"phase"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	BytesReceived int       `json:"bytes_received"`
}

func (c InFlightCall) brief() string {
	var b strings.Builder
	if c.CallID != "" {
		b.WriteString(c.CallID)
	} else {
		b.WriteString("call")
	}
	if c.Seq > 0 {
		fmt.Fprintf(&b, " seq=%d", c.Seq)
	}
	if c.APIFamily != "" {
		fmt.Fprintf(&b, " %s", c.APIFamily)
	} else if c.Path != "" {
		fmt.Fprintf(&b, " %s", c.Path)
	}
	if c.Phase != "" {
		fmt.Fprintf(&b, " %s", c.Phase)
	}
	if c.HTTPStatus > 0 {
		fmt.Fprintf(&b, " status=%d", c.HTTPStatus)
	}
	if c.ElapsedMS > 0 {
		fmt.Fprintf(&b, " %dms", c.ElapsedMS)
	}
	return b.String()
}

func formatInFlightList(calls []InFlightCall) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, len(calls))
	for _, c := range calls {
		parts = append(parts, c.brief())
	}
	return strings.Join(parts, "; ")
}

// WaitTimeoutError is a 504 wait_timeout from /sessions/{id}/wait.
type WaitTimeoutError struct {
	SessionID   string
	Timeout     time.Duration
	Calls       []Call
	CallsSeen   int
	MinCalls    int
	Active      int
	ActiveKnown bool
	InFlight    []InFlightCall
}

func (e *WaitTimeoutError) Error() string {
	if e == nil {
		return "wait timed out"
	}
	n := e.CallsSeen
	if n == 0 {
		n = len(e.Calls)
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	soFar := fmt.Sprintf("%d calls so far", n)
	if e.ActiveKnown {
		soFar += fmt.Sprintf(", still active: %d", e.Active)
		if detail := formatInFlightList(e.InFlight); detail != "" {
			soFar += " [" + detail + "]"
		}
	}
	msg := fmt.Sprintf("wait timed out after %s on session %s (%s). Raise %s if the graph needs more time",
		formatWaitDuration(timeout), e.SessionID, soFar, EnvWaitTimeout)
	if e.MinCalls > 0 {
		msg += fmt.Sprintf(" (min_calls=%d)", e.MinCalls)
	}
	return msg
}

type statusError struct {
	op          string
	status      int
	body        string
	typ         string
	message     string
	param       string
	session     sessionPayload
	callsSeen   int
	minCalls    int
	active      *int
	inFlight    []InFlightCall
	inFlightSet bool
}

func (e *statusError) Error() string {
	return fmt.Sprintf("llmentary %s failed: status=%d body=%s", e.op, e.status, e.body)
}

func parseStatusError(op string, status int, body string) *statusError {
	se := &statusError{op: op, status: status, body: body}
	if strings.TrimSpace(body) == "" {
		return se
	}
	var envelope struct {
		Error struct {
			Type      string          `json:"type"`
			Message   string          `json:"message"`
			Param     string          `json:"param"`
			CallsSeen int             `json:"calls_seen"`
			MinCalls  int             `json:"min_calls"`
			Active    *int            `json:"active"`
			InFlight  json.RawMessage `json:"in_flight"`
		} `json:"error"`
		ID    string `json:"id"`
		Calls []Call `json:"calls"`
	}
	if json.Unmarshal([]byte(body), &envelope) != nil {
		return se
	}
	se.typ = envelope.Error.Type
	se.message = envelope.Error.Message
	se.param = envelope.Error.Param
	se.callsSeen = envelope.Error.CallsSeen
	se.minCalls = envelope.Error.MinCalls
	se.active = envelope.Error.Active
	if envelope.Error.InFlight != nil {
		var inflight []InFlightCall
		if json.Unmarshal(envelope.Error.InFlight, &inflight) == nil {
			se.inFlight = inflight
			se.inFlightSet = true
		}
	}
	se.session = sessionPayload{ID: envelope.ID, Calls: envelope.Calls}
	return se
}

func (e *statusError) waitInFlight() (active int, known bool, calls []InFlightCall) {
	if e == nil {
		return 0, false, nil
	}
	if e.active != nil {
		known = true
		active = *e.active
	}
	if e.inFlightSet {
		calls = e.inFlight
		if !known {
			known = true
			active = len(e.inFlight)
		}
	}
	return active, known, calls
}

func (e *statusError) sessionNotFound() bool {
	if e == nil {
		return false
	}
	if e.status == http.StatusNotFound && e.typ == errorTypeSessionNotFound {
		return true
	}
	return e.status == http.StatusNotFound && strings.Contains(strings.ToLower(e.body), "no calls recorded")
}

func newSessionNotFound(sessionID string, known []string, err error) *SessionNotFoundError {
	return &SessionNotFoundError{SessionID: sessionID, KnownIDs: known, Err: err}
}
