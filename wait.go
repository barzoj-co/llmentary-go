// SPDX-License-Identifier: MIT

package llmentary

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultWaitTimeout is the proxy-matching wait timeout sent as `timeout=30s`.
	DefaultWaitTimeout = 30 * time.Second
	// DefaultWaitQuiet is the proxy-matching quiet period sent as `quiet=500ms`.
	DefaultWaitQuiet = 500 * time.Millisecond

	// EnvWaitTimeout overrides DefaultWaitTimeout. Accepts a Go duration or bare seconds.
	EnvWaitTimeout = "LLMENTARY_WAIT_TIMEOUT"
	// EnvWaitQuiet overrides DefaultWaitQuiet. Accepts a Go duration or bare seconds.
	EnvWaitQuiet = "LLMENTARY_WAIT_QUIET"

	envWaitTimeoutMSDeprecated = "LLMENTARY_WAIT_TIMEOUT_MS"
	envWaitIdleMSDeprecated    = "LLMENTARY_WAIT_IDLE_MS"

	waitHTTPPadding = 5 * time.Second
)

var deprecatedWaitEnvOnce sync.Once

func formatWaitDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	ms := d / time.Millisecond
	if rem := d % time.Millisecond; rem >= time.Millisecond/2 {
		ms++
	}
	if ms%1000 == 0 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%dms", ms)
}

// WaitOptions configures /sessions/{id}/wait beyond quiet/timeout.
type WaitOptions struct {
	MinCalls   int
	Since      *Mark
	AllowEmpty bool
}

// Mark is a seq checkpoint from Session.Mark.
type Mark struct {
	Seq int
}

// WaitOption mutates WaitOptions. Used by Client.Wait, Session.Wait,
// Session.Expect, and Session.Check.
type WaitOption func(*WaitOptions)

// MinCalls sets wait?min_calls=N. The proxy returns only after at least N
// finished calls and the quiet period. Default 0 (omit the parameter).
func MinCalls(n int) WaitOption {
	return func(o *WaitOptions) { o.MinCalls = n }
}

// Since scopes Expect/Check to calls after mark.Seq. It is not sent to the proxy.
func Since(m Mark) WaitOption {
	return func(o *WaitOptions) {
		cp := m
		o.Since = &cp
	}
}

// AllowEmpty lets NoErrors, ToolNotCalled, and IsolatedFrom pass when a
// filter, Since, or compared session matches zero calls. It is not sent
// to the proxy.
func AllowEmpty() WaitOption {
	return func(o *WaitOptions) { o.AllowEmpty = true }
}

func applyWaitOptions(opts []WaitOption) WaitOptions {
	var o WaitOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// waitQuery is the exact control-API query string. Parameter order is
// timeout, quiet, then min_calls so it matches the other clients.
func waitQuery(quiet, timeout time.Duration, minCalls int) string {
	var parts []string
	if timeout > 0 {
		parts = append(parts, "timeout="+formatWaitDuration(timeout))
	}
	if quiet > 0 {
		parts = append(parts, "quiet="+formatWaitDuration(quiet))
	}
	if minCalls > 0 {
		parts = append(parts, fmt.Sprintf("min_calls=%d", minCalls))
	}
	return strings.Join(parts, "&")
}

func waitHTTPTimeout(quiet, timeout time.Duration) time.Duration {
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	if quiet <= 0 {
		quiet = DefaultWaitQuiet
	}
	return quiet + timeout + waitHTTPPadding
}

func parseWaitDuration(name, value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%s: invalid duration %q (want a Go duration like 45s or bare seconds like 45)", name, value)
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d < 0 {
			return 0, fmt.Errorf("%s: invalid duration %q (must be non-negative)", name, value)
		}
		return d, nil
	}
	secs, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q (want a Go duration like 45s or bare seconds like 45)", name, value)
	}
	if secs < 0 {
		return 0, fmt.Errorf("%s: invalid duration %q (must be non-negative)", name, value)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

func parseWaitDurationMillis(name, value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%s: invalid duration %q (want milliseconds like 45000 or a Go duration)", name, value)
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d < 0 {
			return 0, fmt.Errorf("%s: invalid duration %q (must be non-negative)", name, value)
		}
		return d, nil
	}
	ms, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q (want milliseconds like 45000 or a Go duration)", name, value)
	}
	if ms < 0 {
		return 0, fmt.Errorf("%s: invalid duration %q (must be non-negative)", name, value)
	}
	return time.Duration(ms * float64(time.Millisecond)), nil
}

func warnDeprecatedWaitEnv(name string) {
	deprecatedWaitEnvOnce.Do(func() {
		fmt.Fprintf(os.Stderr, "llmentary: %s is deprecated; use %s and %s\n", name, EnvWaitTimeout, EnvWaitQuiet)
	})
}

func applyWaitEnv(quiet, timeout *time.Duration) error {
	if raw := os.Getenv(envWaitTimeoutMSDeprecated); raw != "" {
		warnDeprecatedWaitEnv(envWaitTimeoutMSDeprecated)
		d, err := parseWaitDurationMillis(envWaitTimeoutMSDeprecated, raw)
		if err != nil {
			return err
		}
		*timeout = d
	}
	if raw := os.Getenv(envWaitIdleMSDeprecated); raw != "" {
		warnDeprecatedWaitEnv(envWaitIdleMSDeprecated)
		d, err := parseWaitDurationMillis(envWaitIdleMSDeprecated, raw)
		if err != nil {
			return err
		}
		*quiet = d
	}
	if raw := os.Getenv(EnvWaitTimeout); raw != "" {
		d, err := parseWaitDuration(EnvWaitTimeout, raw)
		if err != nil {
			return err
		}
		*timeout = d
	}
	if raw := os.Getenv(EnvWaitQuiet); raw != "" {
		d, err := parseWaitDuration(EnvWaitQuiet, raw)
		if err != nil {
			return err
		}
		*quiet = d
	}
	return nil
}

func countActiveCalls(calls []Call) int {
	n := 0
	for _, call := range calls {
		if call.EndedAt.IsZero() {
			n++
		}
	}
	return n
}

func waitTimeoutError(sessionID string, timeout time.Duration, calls []Call, minCalls, callsSeen int) error {
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	if callsSeen == 0 {
		callsSeen = len(calls)
	}
	return &WaitTimeoutError{
		SessionID: sessionID,
		Timeout:   timeout,
		Calls:     calls,
		CallsSeen: callsSeen,
		MinCalls:  minCalls,
	}
}
