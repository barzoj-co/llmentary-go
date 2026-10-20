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
		if strings.Contains(hay, needle) || strings.Contains(hay, trimDegreeUnit(needle)) || strings.Contains(trimDegreeUnit(hay), needle) {
			return true
		}
	}
	return false
}

func trimDegreeUnit(s string) string {
	s = strings.TrimSpace(s)
	for _, suffix := range []string{" c", " f", "c", "f"} {
		if strings.HasSuffix(s, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(s, suffix))
		}
	}
	return s
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
	if sys, ok := body["system"].(string); ok && sys != "" {
		parts = append(parts, sys)
	}
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
	var callID string
	seen := false
	for _, call := range a.trace.Calls {
		if !seen {
			for _, tool := range call.Normalized.ToolCalls {
				if tool.Name == toolName {
					callID = tool.ID
					seen = true
					break
				}
			}
			continue
		}
		content := toolResultContent(call.RawRequest, callID, toolName)
		if content == "" {
			continue
		}
		if strings.Contains(call.PromptText(), content) {
			return true
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
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	for _, item := range items {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		typ, _ := msg["type"].(string)
		isResult := role == "tool" || typ == "function_call_output" || typ == "tool_result"
		if !isResult {
			continue
		}
		id, _ := msg["tool_call_id"].(string)
		if id == "" {
			id, _ = msg["call_id"].(string)
		}
		toolName, _ := msg["name"].(string)
		idMatch := callID != "" && id == callID
		nameMatch := name != "" && toolName == name
		if !idMatch && !nameMatch {
			continue
		}
		if content, ok := msg["content"].(string); ok && content != "" {
			return content
		}
		if output, ok := msg["output"].(string); ok && output != "" {
			return output
		}
	}
	return ""
}
