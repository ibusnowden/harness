package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func normalizeToolCallArgumentsRaw(raw json.RawMessage, toolName, toolID string) (json.RawMessage, error) {
	return normalizeToolCallArgumentsString(string(raw), toolName, toolID)
}

func normalizeToolCallArgumentsString(raw, toolName, toolID string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return json.RawMessage(`{}`), nil
	}
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(raw)); err != nil {
		return nil, fmt.Errorf("%s arguments are not valid JSON: %w (snippet: %s)", formatToolCallLabel(toolName, toolID), err, summarizeToolCallSnippet(raw))
	}
	normalized := out.Bytes()
	var value any
	if err := json.Unmarshal(normalized, &value); err != nil {
		return nil, fmt.Errorf("%s arguments are not valid JSON: %w", formatToolCallLabel(toolName, toolID), err)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("%s arguments must be a JSON object", formatToolCallLabel(toolName, toolID))
	}
	copyBytes := make([]byte, len(normalized))
	copy(copyBytes, normalized)
	return json.RawMessage(copyBytes), nil
}

func formatToolCallLabel(toolName, toolID string) string {
	parts := []string{"tool call"}
	if trimmed := strings.TrimSpace(toolName); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(toolID); trimmed != "" {
		parts = append(parts, "("+trimmed+")")
	}
	return strings.Join(parts, " ")
}

func summarizeToolCallSnippet(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return `""`
	}
	runes := []rune(trimmed)
	if len(runes) > 120 {
		trimmed = string(runes[:120]) + "..."
	}
	return fmt.Sprintf("%q", trimmed)
}
