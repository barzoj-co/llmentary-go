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
// session_not_found, or when Expect/Check runs on a session that never
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
	if len(e.KnownIDs) > 0 {
		msg += "; known sessions: " + strings.Join(e.KnownIDs, ", ")
	} else {
		msg += "; known sessions: GET /sessions"
	}
	return msg
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

// WaitTimeoutError is a 504 wait_timeout from /sessions/{id}/wait.
type WaitTimeoutError struct {
	SessionID string
	Timeout   time.Duration
	Calls     []Call
	CallsSeen int
	MinCalls  int
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
	msg := fmt.Sprintf("wait timed out after %s on session %s (%d calls so far, still active: %d). Raise %s if the graph needs more time",
		formatWaitDuration(timeout), e.SessionID, n, countActiveCalls(e.Calls), EnvWaitTimeout)
	if e.MinCalls > 0 {
		msg += fmt.Sprintf(" (min_calls=%d)", e.MinCalls)
	}
	return msg
}

type statusError struct {
	op        string
	status    int
	body      string
	typ       string
	message   string
	param     string
	session   sessionPayload
	callsSeen int
	minCalls  int
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
			Type      string `json:"type"`
			Message   string `json:"message"`
			Param     string `json:"param"`
			CallsSeen int    `json:"calls_seen"`
			MinCalls  int    `json:"min_calls"`
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
	se.session = sessionPayload{ID: envelope.ID, Calls: envelope.Calls}
	return se
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
