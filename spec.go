// SPDX-License-Identifier: MIT

package llmentary

import (
	"fmt"
	"strings"
	"testing"
)

// Validation returns the per-call spec-check metadata, or nil if absent.
func (c Call) Validation() *Validation {
	return c.Normalized.Validation
}

// SpecTarget returns normalized.validation.target (openai or
// openai-compatible), or "" when validation is absent or the field is empty.
func (c Call) SpecTarget() string {
	if v := c.Validation(); v != nil {
		return v.Target
	}
	return ""
}

// SpecViolations returns the findings and whether validation ran.
// bool is false when the spec_violations key is absent (not checked).
// An empty slice with true is clean.
func (c Call) SpecViolations() ([]SpecViolation, bool) {
	if c.Normalized.SpecViolations == nil {
		return nil, false
	}
	return append([]SpecViolation(nil), *c.Normalized.SpecViolations...), true
}

// IgnoredFields returns ignored vendor fields and whether the list was present.
func (c Call) IgnoredFields() ([]IgnoredField, bool) {
	if c.Normalized.IgnoredFields == nil {
		return nil, false
	}
	return append([]IgnoredField(nil), *c.Normalized.IgnoredFields...), true
}

// SpecViolations concatenates findings from calls that were checked.
func (tr Trace) SpecViolations() []SpecViolation {
	var out []SpecViolation
	for _, call := range tr.Calls {
		if findings, ok := call.SpecViolations(); ok {
			out = append(out, findings...)
		}
	}
	return out
}

// IgnoredFields concatenates ignored-field entries from calls that recorded them.
func (tr Trace) IgnoredFields() []IgnoredField {
	var out []IgnoredField
	for _, call := range tr.Calls {
		if findings, ok := call.IgnoredFields(); ok {
			out = append(out, findings...)
		}
	}
	return out
}

// SpecStatusLabel is the same token the proxy log uses: clean,
// request_only, N violations, not checked, or skipped:<reason>.
func (c Call) SpecStatusLabel() string {
	return c.specSummary()
}

func (c Call) specSummary() string {
	v := c.Normalized.Validation
	if v != nil && strings.TrimSpace(v.SkippedReason) != "" {
		return "skipped:" + strings.TrimSpace(v.SkippedReason)
	}
	findings, _ := c.SpecViolations()
	if len(findings) == 1 {
		return "1 violation"
	}
	if len(findings) > 1 {
		return fmt.Sprintf("%d violations", len(findings))
	}
	if v == nil {
		return "not checked"
	}
	if v.RequestChecked && v.ResponseChecked {
		return "clean"
	}
	if v.RequestChecked && !v.ResponseChecked {
		return "request_only"
	}
	return "not checked"
}

// SpecOption configures NoSpecViolations / NoIgnoredFields.
// Contract names: Side, Calls, IgnorePaths, IgnoreKeywords, IgnoreKinds,
// MinSeverity, plus AllowUnvalidated.
type SpecOption func(*specOptions)

type specOptions struct {
	Side             string
	Seqs             []int
	IgnorePaths      []string
	IgnoreKeywords   []string
	MinSeverity      string
	AllowUnvalidated bool
}

// Side limits findings to request or response.
func Side(side string) SpecOption {
	return func(o *specOptions) { o.Side = side }
}

// Calls limits the assertion to the given 1-based call sequence numbers.
func Calls(seqs ...int) SpecOption {
	return func(o *specOptions) { o.Seqs = append([]int(nil), seqs...) }
}

// IgnorePaths skips findings whose path or JSON pointer matches.
func IgnorePaths(paths ...string) SpecOption {
	return func(o *specOptions) { o.IgnorePaths = append([]string(nil), paths...) }
}

// IgnoreKeywords skips findings whose keyword or kind matches.
// On ignored_fields this is the same filter as IgnoreKinds (DOG-55).
func IgnoreKeywords(keywords ...string) SpecOption {
	return func(o *specOptions) { o.IgnoreKeywords = append(o.IgnoreKeywords, keywords...) }
}

// IgnoreKinds is the kind-named alias of IgnoreKeywords (Python
// ignore_kinds / JS ignoreKinds). Both match keyword or kind.
func IgnoreKinds(kinds ...string) SpecOption {
	return IgnoreKeywords(kinds...)
}

// MinSeverity skips ignored_fields below this rank (info < warning <
// error). Empty (the default) keeps every severity so existing
// NoIgnoredFields calls stay unchanged. A missing or unknown entry
// severity is fail-closed (kept). Python: min_severity; JS: minSeverity.
func MinSeverity(level string) SpecOption {
	return func(o *specOptions) { o.MinSeverity = strings.TrimSpace(level) }
}

// AllowUnvalidated passes when validation was not run (mode=off or absent).
func AllowUnvalidated() SpecOption {
	return func(o *specOptions) { o.AllowUnvalidated = true }
}

func applySpecOptions(opts []SpecOption) specOptions {
	var o specOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// NoSpecViolations is AssertNoSpecViolations from docs/client-contract.md.
func (a *Assertions) NoSpecViolations(opts ...SpecOption) bool {
	a.t.Helper()
	return a.assertNoFindings(true, opts...)
}

// NoIgnoredFields is AssertNoIgnoredFields from docs/client-contract.md.
func (a *Assertions) NoIgnoredFields(opts ...SpecOption) bool {
	a.t.Helper()
	return a.assertNoFindings(false, opts...)
}

// AssertNoSpecViolations is the documented contract name for NoSpecViolations.
func AssertNoSpecViolations(t testing.TB, trace Trace, opts ...SpecOption) {
	t.Helper()
	(&Assertions{t: t, trace: trace, fatal: true}).NoSpecViolations(opts...)
}

// AssertNoIgnoredFields is the documented contract name for NoIgnoredFields.
func AssertNoIgnoredFields(t testing.TB, trace Trace, opts ...SpecOption) {
	t.Helper()
	(&Assertions{t: t, trace: trace, fatal: true}).NoIgnoredFields(opts...)
}

func (a *Assertions) assertNoFindings(spec bool, opts ...SpecOption) bool {
	a.t.Helper()
	options := applySpecOptions(opts)
	calls, err := selectSpecCalls(a.trace.Calls, options)
	if err != nil {
		return a.fail("%s", err.Error())
	}
	var lines []string
	checked := 0
	for _, call := range calls {
		if spec {
			findings, ok := call.SpecViolations()
			if !ok {
				continue
			}
			checked++
			for _, finding := range findings {
				if skipSpecFinding(finding.Side, finding.Path, finding.Pointer, finding.Keyword, "", "", options) {
					continue
				}
				lines = append(lines, formatSpecFinding(call, finding.Side, finding.Path, finding.Pointer, finding.Keyword, finding.Message))
			}
			continue
		}
		findings, ok := call.IgnoredFields()
		if !ok {
			continue
		}
		checked++
		for _, finding := range findings {
			side := finding.FilterSide()
			if skipSpecFinding(side, finding.Path, finding.Pointer, finding.Keyword, finding.Kind, finding.Severity, options) {
				continue
			}
			lines = append(lines, formatSpecFinding(call, side, finding.Path, finding.Pointer, firstNonEmpty(finding.Keyword, finding.Kind), finding.Message))
		}
	}
	if checked == 0 {
		if options.AllowUnvalidated {
			return true
		}
		return a.fail("validation was not run")
	}
	if len(lines) == 0 {
		return true
	}
	return a.fail("%s", strings.Join(lines, "\n"))
}

func selectSpecCalls(calls []Call, options specOptions) ([]Call, error) {
	if len(options.Seqs) == 0 {
		if len(calls) == 0 {
			return nil, fmt.Errorf("validation was not run")
		}
		return calls, nil
	}
	want := map[int]bool{}
	for _, seq := range options.Seqs {
		want[seq] = true
	}
	var out []Call
	for _, call := range calls {
		if want[call.Seq] {
			out = append(out, call)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("validation was not run: no calls match seq %v (trace has %d call(s))", options.Seqs, len(calls))
	}
	return out, nil
}

func skipSpecFinding(side, path, pointer, keyword, kind, severity string, options specOptions) bool {
	if options.Side != "" {
		// Empty side/context is fail-closed: a filter must not skip the entry.
		if side != "" && !strings.EqualFold(side, options.Side) {
			return true
		}
	}
	for _, ignore := range options.IgnorePaths {
		if pathMatches(path, pointer, ignore) {
			return true
		}
	}
	for _, ignore := range options.IgnoreKeywords {
		// An empty filter value must not match an empty keyword or kind.
		if ignore == "" {
			continue
		}
		if strings.EqualFold(keyword, ignore) || strings.EqualFold(kind, ignore) {
			return true
		}
	}
	return belowMinSeverity(severity, options.MinSeverity)
}

func severityRank(level string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "info":
		return 1, true
	case "warning":
		return 2, true
	case "error":
		return 3, true
	default:
		return 0, false
	}
}

// belowMinSeverity is true when the entry is strictly weaker than min.
// Missing/unknown severity is fail-closed (not below).
func belowMinSeverity(severity, min string) bool {
	want, ok := severityRank(min)
	if !ok {
		return false
	}
	got, ok := severityRank(severity)
	if !ok {
		return false
	}
	return got < want
}

func pathMatches(path, pointer, ignore string) bool {
	if ignore == "" {
		return false
	}
	for _, got := range []string{path, pointer} {
		if got == "" {
			continue
		}
		if got == ignore || strings.TrimPrefix(got, "/") == strings.TrimPrefix(ignore, "/") {
			return true
		}
		if segmentPrefix(got, ignore) || segmentSuffix(got, ignore) {
			return true
		}
	}
	return false
}

func segmentPrefix(got, ignore string) bool {
	if !strings.HasPrefix(got, ignore) {
		return false
	}
	rest := got[len(ignore):]
	return rest == "" || rest[0] == '.' || rest[0] == '/' ||
		strings.HasSuffix(ignore, ".") || strings.HasSuffix(ignore, "/")
}

func segmentSuffix(got, ignore string) bool {
	if !strings.HasSuffix(got, ignore) {
		return false
	}
	head := got[:len(got)-len(ignore)]
	return head == "" || strings.HasSuffix(head, ".") || strings.HasSuffix(head, "/") ||
		strings.HasPrefix(ignore, ".") || strings.HasPrefix(ignore, "/")
}

func formatSpecFinding(call Call, side, path, pointer, keyword, message string) string {
	displayPath := path
	if displayPath == "" {
		displayPath = pointer
	}
	if displayPath == "" {
		displayPath = "-"
	}
	hint := keyword
	if hint == "" {
		hint = "spec"
	}
	if message == "" {
		message = hint
	}
	api := call.APIFamily
	if api == "" {
		api = call.Provider
	}
	if side == "" {
		side = "-"
	}
	return fmt.Sprintf("#%d %s %s %s: %s — %s", call.Seq, api, side, displayPath, message, hint)
}
