// SPDX-License-Identifier: MIT

package llmentary

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"
)

const defaultIsolationMinChars = 8

type isolatedFromConfig struct {
	requirePath bool
	other       *Trace
	minChars    int
	minCharsSet bool
	ignore      []string
}

func (c isolatedFromConfig) minCharsOrDefault() int {
	if c.minCharsSet {
		return c.minChars
	}
	return defaultIsolationMinChars
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

// MinChars sets the isolation text length floor (default 8). Zero keeps
// short texts. Applied after trim / whitespace collapse / casefold.
func MinChars(n int) IsolatedFromOption {
	return func(c *isolatedFromConfig) {
		c.minChars = n
		c.minCharsSet = true
	}
}

// IgnoreTexts skips these user messages after the same normalization as
// IsolatedFrom matching.
func IgnoreTexts(texts ...string) IsolatedFromOption {
	return func(c *isolatedFromConfig) {
		c.ignore = append(c.ignore, texts...)
	}
}

// IsolatedFrom checks that this session did not leak into otherSessionID.
//
// Rules (docs/isolation-spec.md on the proxy):
//  1. every call's session_id is this session (and the other trace matches);
//  2. no call id appears in both sessions;
//  3. no foreign user turn: a user-role message in this request that this
//     session never sent (last user message of each of its calls) but the
//     other session did. Comparison is exact after trim, whitespace
//     collapse, and casefold — never substring.
//
// Empty sessions fail unless AllowEmpty. System/developer prompts and
// tool definitions are never compared.
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
	ignore := isolationIgnoreSet(cfg.ignore)
	minChars := cfg.minCharsOrDefault()
	sent := this
	if len(a.sessionTrace.Calls) > 0 {
		sent = a.sessionTrace.Calls
	}
	thisSent := introducedUserTurns(thisID, sent, minChars, ignore)
	otherSent := introducedUserTurns(otherSessionID, other.Calls, minChars, ignore)
	for _, call := range this {
		for _, text := range call.UserTexts() {
			key := normalizeIsolationText(text)
			if !keepIsolationText(key, minChars, ignore) {
				continue
			}
			if _, own := thisSent[key]; own {
				continue
			}
			hit, foreign := otherSent[key]
			if !foreign {
				continue
			}
			return a.fail("user text %q from session %s #%d (%s) appears in session %s #%d (%s)",
				text, hit.session, hit.seq, hit.callID, thisID, call.Seq, call.ID)
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

type isolationTurn struct {
	session string
	callID  string
	seq     int
	text    string
}

func introducedUserTurns(sessionID string, calls []Call, minChars int, ignore map[string]struct{}) map[string]isolationTurn {
	ordered := append([]Call(nil), calls...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Seq < ordered[j].Seq })
	out := map[string]isolationTurn{}
	for _, call := range ordered {
		texts := call.UserTexts()
		if len(texts) == 0 {
			continue
		}
		text := texts[len(texts)-1]
		key := normalizeIsolationText(text)
		if !keepIsolationText(key, minChars, ignore) {
			continue
		}
		if _, exists := out[key]; exists {
			continue
		}
		sid := sessionID
		if call.SessionID != "" {
			sid = call.SessionID
		}
		out[key] = isolationTurn{session: sid, callID: call.ID, seq: call.Seq, text: text}
	}
	return out
}

func isolationIgnoreSet(texts []string) map[string]struct{} {
	if len(texts) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(texts))
	for _, text := range texts {
		key := normalizeIsolationText(text)
		if key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}

func keepIsolationText(key string, minChars int, ignore map[string]struct{}) bool {
	if key == "" {
		return false
	}
	if _, skip := ignore[key]; skip {
		return false
	}
	if minChars > 0 && utf8.RuneCountInString(key) < minChars {
		return false
	}
	return true
}

func normalizeIsolationText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}
