package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

var previewTags = regexp.MustCompile(`<[^>]+>`)

// Metadata snapshots replace the old global raw-line stream. Carry the first
// Claude user-message preview in metadata so unselected sessions still show it.
func claudePreview(line string) string {
	var event struct {
		Type          string          `json:"type"`
		IsMeta        bool            `json:"isMeta"`
		ToolUseResult json.RawMessage `json:"toolUseResult"`
		Message       struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &event) != nil || event.Type != "user" || event.IsMeta {
		return ""
	}
	if len(event.ToolUseResult) > 0 && string(event.ToolUseResult) != "null" && string(event.ToolUseResult) != "false" {
		return ""
	}
	content := strings.TrimSpace(previewTags.ReplaceAllString(claudeText(event.Message.Content), ""))
	first, _, _ := strings.Cut(content, "\n")
	return first
}

func claudeText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "tool_result":
			parts = append(parts, claudeText(block.Content))
		}
	}
	return strings.Join(parts, "\n")
}
