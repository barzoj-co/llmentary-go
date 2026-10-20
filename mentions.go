// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const mentionExcerptLimit = 200

// MentionsAny reports whether text mentions any candidate. Numbers and case
// are normalized by default so "20°C", "20 °C", and "20 degrees" match.
func MentionsAny(text string, candidates ...string) bool {
	return mentionsAny(text, candidates, true, true)
}

func mentionsAny(text string, candidates []string, normalizeNumbers, caseFold bool) bool {
	hay := normalizeMention(text, normalizeNumbers, caseFold)
	for _, candidate := range candidates {
		needle := normalizeMention(candidate, normalizeNumbers, caseFold)
		if needle == "" {
			continue
		}
		if mentionTokensContain(hay, needle) || mentionTokensContain(hay, trimDegreeUnit(needle)) || mentionTokensContain(trimDegreeUnit(hay), needle) {
			return true
		}
	}
	return false
}

func trimDegreeUnit(s string) string {
	s = strings.TrimSpace(s)
	for _, suffix := range []string{" c", " f"} {
		if !strings.HasSuffix(s, suffix) {
			continue
		}
		stem := strings.TrimSpace(strings.TrimSuffix(s, suffix))
		tokens := mentionTokens(stem)
		if len(tokens) == 0 {
			continue
		}
		last := tokens[len(tokens)-1]
		if last == "degrees" || isNumericToken(last) {
			return stem
		}
	}
	return s
}

func mentionTokens(s string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		if r == '.' && b.Len() > 0 && i+1 < len(runes) && unicode.IsDigit(runes[i+1]) {
			last := rune(0)
			for _, tr := range b.String() {
				last = tr
			}
			if unicode.IsDigit(last) {
				b.WriteRune(r)
				continue
			}
		}
		flush()
	}
	flush()
	return tokens
}

func mentionTokensContain(hay, needle string) bool {
	ht := mentionTokens(hay)
	nt := mentionTokens(needle)
	if len(nt) == 0 || len(nt) > len(ht) {
		return false
	}
	for i := 0; i+len(nt) <= len(ht); i++ {
		match := true
		for j := range nt {
			if ht[i+j] != nt[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func isNumericToken(s string) bool {
	if s == "" {
		return false
	}
	sawDigit := false
	for i, r := range s {
		if unicode.IsDigit(r) {
			sawDigit = true
			continue
		}
		if r == '.' && i > 0 && i < len(s)-1 {
			continue
		}
		return false
	}
	return sawDigit
}

func normalizeMention(s string, normalizeNumbers, caseFold bool) string {
	if caseFold {
		s = strings.ToLower(s)
	}
	s = strings.NewReplacer("℃", " degrees c", "°", " degrees ", "º", " degrees ").Replace(s)
	if normalizeNumbers {
		s = insertNumberBoundaries(s)
	}
	return strings.Join(strings.Fields(s), " ")
}

func insertNumberBoundaries(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	prev := rune(0)
	for i, r := range s {
		digit := unicode.IsDigit(r)
		prevDigit := unicode.IsDigit(prev)
		if i > 0 && prevDigit && !digit && !unicode.IsSpace(r) && r != '.' {
			b.WriteByte(' ')
		}
		if i > 0 && digit && !prevDigit && !unicode.IsSpace(prev) && prev != '.' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

func mentionMissMessage(text string, candidates []string) string {
	excerpt := text
	if len(excerpt) > mentionExcerptLimit {
		excerpt = excerpt[:mentionExcerptLimit]
	}
	return fmt.Sprintf("did not mention any of %v; excerpt: %q", candidates, excerpt)
}

// PromptText is the request prompt reconstructed from RawRequest, else system_prompt.
func (c Call) PromptText() string {
	if text := promptFromRawRequest(c.RawRequest); text != "" {
		return text
	}
	return c.Normalized.SystemPrompt
}

// ResponseText is normalized.output_text.
func (c Call) ResponseText() string {
	return c.Normalized.OutputText
}

func promptFromRawRequest(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return string(raw)
	}
	var parts []string
	appendContent(&parts, body["system"])
	appendContent(&parts, body["instructions"])
	appendContent(&parts, body["messages"])
	appendContent(&parts, body["input"])
	return strings.Join(parts, "\n")
}

func appendContent(parts *[]string, value any) {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			*parts = append(*parts, typed)
		}
	case []any:
		for _, item := range typed {
			switch msg := item.(type) {
			case string:
				if msg != "" {
					*parts = append(*parts, msg)
				}
			case map[string]any:
				if content, ok := msg["content"].(string); ok && content != "" {
					*parts = append(*parts, content)
				}
				if output, ok := msg["output"].(string); ok && output != "" {
					*parts = append(*parts, output)
				}
				if text, ok := msg["text"].(string); ok && text != "" {
					*parts = append(*parts, text)
				}
				appendContent(parts, msg["content"])
			}
		}
	}
}

func (a *Assertions) PromptContains(substr string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if strings.Contains(call.PromptText(), substr) {
			return true
		}
	}
	return a.fail("expected prompt to contain %q", substr)
}

// FinalResponseMentionsAny checks the last recorded output_text with MentionsAny.
func (a *Assertions) FinalResponseMentionsAny(candidates ...string) bool {
	a.t.Helper()
	text := ""
	if n := len(a.trace.Calls); n > 0 {
		text = a.trace.Calls[n-1].ResponseText()
	}
	if MentionsAny(text, candidates...) {
		return true
	}
	return a.fail("%s", mentionMissMessage(text, candidates))
}

// ToolResultInPrompt checks that a request after toolName carries that tool's
// result content verbatim (stronger, name-based form of ToolResultFedBack).
func (a *Assertions) ToolResultInPrompt(toolName string) bool {
	a.t.Helper()
	for i, call := range a.trace.Calls {
		for _, tool := range call.Normalized.ToolCalls {
			if tool.Name != toolName {
				continue
			}
			for _, next := range a.trace.Calls[i+1:] {
				content := toolResultContent(next.RawRequest, tool.ID, toolName)
				if content != "" && strings.Contains(next.PromptText(), content) {
					return true
				}
			}
		}
	}
	return a.fail("expected the next request after tool %q to carry its result verbatim", toolName)
}

func toolResultContent(raw []byte, callID, name string) string {
	if len(raw) == 0 {
		return ""
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	if got := findToolResult(body["messages"], callID, name); got != "" {
		return got
	}
	return findToolResult(body["input"], callID, name)
}

func findToolResult(value any, callID, name string) string {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if got := findToolResult(item, callID, name); got != "" {
				return got
			}
		}
	case map[string]any:
		if nested, ok := typed["content"].([]any); ok {
			if got := findToolResult(nested, callID, name); got != "" {
				return got
			}
		}
		role, _ := typed["role"].(string)
		typ, _ := typed["type"].(string)
		isResult := role == "tool" || typ == "function_call_output" || typ == "tool_result"
		if !isResult {
			return ""
		}
		id, _ := typed["tool_call_id"].(string)
		if id == "" {
			id, _ = typed["call_id"].(string)
		}
		if id == "" {
			id, _ = typed["tool_use_id"].(string)
		}
		toolName, _ := typed["name"].(string)
		if callID != "" && id != "" {
			if id != callID {
				return ""
			}
		} else if name == "" || toolName != name {
			return ""
		}
		var parts []string
		appendContent(&parts, typed["content"])
		appendContent(&parts, typed["output"])
		return strings.Join(parts, "\n")
	}
	return ""
}
