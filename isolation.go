// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"strings"
)

type isolatedFromConfig struct {
	requirePath bool
	other       *Trace
}

// IsolatedFromOption configures IsolatedFrom.
type IsolatedFromOption func(*isolatedFromConfig)

// RequirePathSource requires every call in this session to have
// session_source=path.
func RequirePathSource() IsolatedFromOption {
	return func(c *isolatedFromConfig) { c.requirePath = true }
}

// WithOtherTrace supplies the other session's calls (fixture tests).
// Live tests omit it and IsolatedFrom fetches GET /sessions/{id}.
func WithOtherTrace(tr Trace) IsolatedFromOption {
	return func(c *isolatedFromConfig) {
		cp := tr
		c.other = &cp
	}
}

// IsolatedFrom checks that this session did not leak into otherSessionID.
// Failures name the broken rule: session_id mismatch, shared call id,
// overlapping user-role text, or an empty session (unless AllowEmpty).
func (a *Assertions) IsolatedFrom(otherSessionID string, opts ...IsolatedFromOption) bool {
	a.t.Helper()
	var cfg isolatedFromConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	thisID := a.sessionID()
	this := a.isolationCalls()
	other, ok := a.otherIsolationTrace(otherSessionID, cfg.other)
	if !ok {
		return false
	}
	if !a.requireIsolationCalls(thisID, otherSessionID, this, other.Calls) {
		return false
	}
	for _, call := range this {
		if call.SessionID != "" && call.SessionID != thisID {
			return a.fail("call %s has session_id=%s, want %s", call.ID, call.SessionID, thisID)
		}
		if cfg.requirePath && call.SessionSource != "path" {
			src := call.SessionSource
			if src == "" {
				src = "empty"
			}
			return a.fail("call %s has session_source=%s, want path", call.ID, src)
		}
	}
	for _, call := range other.Calls {
		if call.SessionID != "" && call.SessionID != otherSessionID {
			return a.fail("call %s has session_id=%s, want %s", call.ID, call.SessionID, otherSessionID)
		}
	}
	thisIDs := map[string]struct{}{}
	for _, call := range this {
		if call.ID == "" {
			continue
		}
		thisIDs[call.ID] = struct{}{}
	}
	for _, call := range other.Calls {
		if call.ID == "" {
			continue
		}
		if _, dup := thisIDs[call.ID]; dup {
			return a.fail("call id %s appears in sessions %s and %s", call.ID, thisID, otherSessionID)
		}
	}
	type userHit struct {
		session string
		callID  string
		text    string
	}
	var thisUsers, otherUsers []userHit
	for _, call := range this {
		for _, text := range call.UserTexts() {
			if strings.TrimSpace(text) == "" {
				continue
			}
			thisUsers = append(thisUsers, userHit{session: thisID, callID: call.ID, text: text})
		}
	}
	for _, call := range other.Calls {
		for _, text := range call.UserTexts() {
			if strings.TrimSpace(text) == "" {
				continue
			}
			sid := otherSessionID
			if call.SessionID != "" {
				sid = call.SessionID
			}
			otherUsers = append(otherUsers, userHit{session: sid, callID: call.ID, text: text})
		}
	}
	for _, left := range thisUsers {
		for _, right := range otherUsers {
			if userTextAppearsIn(left.text, right.text) {
				return a.fail("user text %q from session %s call %s appears in session %s call %s",
					left.text, left.session, left.callID, right.session, right.callID)
			}
		}
	}
	return true
}

func (a *Assertions) sessionID() string {
	if a.sessionTrace.SessionID != "" {
		return a.sessionTrace.SessionID
	}
	return a.trace.SessionID
}

func (a *Assertions) isolationCalls() []Call {
	if len(a.trace.Calls) > 0 || len(a.filters) > 0 {
		return a.trace.Calls
	}
	if len(a.sessionTrace.Calls) > 0 {
		return a.sessionTrace.Calls
	}
	return a.trace.Calls
}

func (a *Assertions) otherIsolationTrace(otherSessionID string, supplied *Trace) (Trace, bool) {
	a.t.Helper()
	if supplied != nil {
		tr := *supplied
		if tr.SessionID == "" {
			tr.SessionID = otherSessionID
		}
		return tr, true
	}
	if a.client == nil {
		a.fail("IsolatedFrom(%q) needs WithOtherTrace or a Session from New(t)", otherSessionID)
		return Trace{}, false
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	calls, err := a.client.Calls(ctx, otherSessionID)
	if err != nil {
		if IsSessionNotFound(err) {
			return Trace{SessionID: otherSessionID}, true
		}
		a.fail("IsolatedFrom: fetch session %s: %v", otherSessionID, err)
		return Trace{}, false
	}
	return Trace{SessionID: otherSessionID, Calls: calls}, true
}

func (a *Assertions) requireIsolationCalls(thisID, otherID string, this, other []Call) bool {
	a.t.Helper()
	if a.allowEmpty {
		return true
	}
	if len(this) == 0 {
		return a.fail("no calls recorded for session %s", thisID)
	}
	if len(other) == 0 {
		return a.fail("no calls recorded for session %s", otherID)
	}
	return true
}

func userTextAppearsIn(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.Contains(b, a) || strings.Contains(a, b)
}
