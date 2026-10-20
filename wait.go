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

// waitQuery is the exact control-API query string. Parameter order is
// timeout then quiet so it matches the other clients and A1/A6.
func waitQuery(quiet, timeout time.Duration) string {
	var parts []string
	if timeout > 0 {
		parts = append(parts, "timeout="+formatWaitDuration(timeout))
	}
	if quiet > 0 {
		parts = append(parts, "quiet="+formatWaitDuration(quiet))
	}
	return strings.Join(parts, "&")
}

func waitHTTPTimeout(quiet, timeout time.Duration) time.Duration {
	d := quiet + timeout + waitHTTPPadding
	if d < waitHTTPPadding {
		return waitHTTPPadding
	}
	return d
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

func waitTimeoutError(sessionID string, timeout time.Duration, calls []Call) error {
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	return fmt.Errorf("wait timed out after %s on session %s (%d calls so far, still active: %d). Raise %s if the graph needs more time.",
		formatWaitDuration(timeout), sessionID, len(calls), countActiveCalls(calls), EnvWaitTimeout)
}
