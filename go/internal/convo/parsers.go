// Format-specific export JSON parsers (FR-MP-09 / FR-MP-10 / FR-MP-11),
// ported from the Rust `conversation_indexer::parsers` module.
//
// Each parser normalizes its format's shape into RawMessage records;
// classification lives in types.go so mining semantics are shared.
package convo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RawMessage is the normalized message record shared by all three parsers.
type RawMessage struct {
	Participant string
	Timestamp   string
	Text        string
	Source      string
}

// extractTextParts flattens the three content shapes the exports use:
//
//	Claude:  a string, or an array of blocks with a "text" field
//	ChatGPT: an object { content_type, parts: [string] }
//	other:   an object with a "text" field
//
// It returns an empty string for any other shape.
func extractTextParts(value json.RawMessage) string {
	if len(bytes.TrimSpace(value)) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(value, &v); err != nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, block := range t {
			if m, ok := block.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		return joinLines(parts)
	case map[string]any:
		if raw, ok := t["parts"].([]any); ok {
			parts := make([]string, 0, len(raw))
			for _, p := range raw {
				if s, ok := p.(string); ok {
					parts = append(parts, s)
				}
			}
			return joinLines(parts)
		}
		if s, ok := t["text"].(string); ok {
			return s
		}
		return ""
	default:
		return ""
	}
}

func joinLines(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}

// ---------------------------------------------------------------- Claude

type claudeExport struct {
	// Conversations is a pointer so a missing root key is distinguishable
	// from an empty array (Rust's serde rejects the former).
	Conversations *[]struct {
		Messages []struct {
			Role    *string         `json:"role"`
			Text    *string         `json:"text"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	} `json:"conversations"`
}

// parseClaudeExport parses `{ "conversations": [ { "messages": [ { role,
// content } ] } ] }`. A message's explicit `text` field wins over its
// `content`; an absent role falls back to "user".
func parseClaudeExport(content []byte) ([]RawMessage, error) {
	var export claudeExport
	if err := json.Unmarshal(content, &export); err != nil {
		return nil, fmt.Errorf("not a Claude export: %w", err)
	}
	if export.Conversations == nil {
		return nil, fmt.Errorf("not a Claude export: missing conversations array")
	}
	var out []RawMessage
	for _, conv := range *export.Conversations {
		for _, msg := range conv.Messages {
			text := ""
			switch {
			case msg.Text != nil:
				text = *msg.Text
			case len(msg.Content) > 0:
				text = extractTextParts(msg.Content)
			}
			participant := "user"
			if msg.Role != nil {
				participant = *msg.Role
			}
			out = append(out, RawMessage{
				Participant: participant,
				Text:        text,
				Source:      "claude",
			})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- ChatGPT

type chatGPTExport struct {
	Mapping *map[string]chatGPTNode `json:"mapping"`
}

type chatGPTNode struct {
	Message *struct {
		Author *struct {
			Role *string `json:"role"`
		} `json:"author"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Mapping map[string]chatGPTNode `json:"mapping"`
}

// parseChatGPTExport parses `{ "mapping": { "<id>": { "message": {
// author.role, content.parts[] } } } }`. Nested `mapping` subtrees
// (conversation nodes that wrap child nodes) are walked recursively.
func parseChatGPTExport(content []byte) ([]RawMessage, error) {
	var export chatGPTExport
	if err := json.Unmarshal(content, &export); err != nil {
		return nil, fmt.Errorf("not a ChatGPT export: %w", err)
	}
	if export.Mapping == nil {
		return nil, fmt.Errorf("not a ChatGPT export: missing mapping object")
	}
	var out []RawMessage
	collectChatGPTNodes(*export.Mapping, &out)
	return out, nil
}

// sortedKeys returns the node ids in sorted order (Rust used a BTreeMap).
func sortedKeys(m map[string]chatGPTNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectChatGPTNodes walks the mapping in sorted key order and appends one
// record per non-empty message.
func collectChatGPTNodes(nodes map[string]chatGPTNode, out *[]RawMessage) {
	for _, id := range sortedKeys(nodes) {
		node := nodes[id]
		if node.Message != nil && len(node.Message.Content) > 0 {
			if text := extractTextParts(node.Message.Content); strings.TrimSpace(text) != "" {
				participant := "user"
				if node.Message.Author != nil && node.Message.Author.Role != nil {
					participant = *node.Message.Author.Role
				}
				*out = append(*out, RawMessage{
					Participant: participant,
					Text:        text,
					Source:      "chatgpt",
				})
			}
		}
		if len(node.Mapping) > 0 {
			collectChatGPTNodes(node.Mapping, out)
		}
	}
}

// ---------------------------------------------------------------- Slack

type slackExport struct {
	Messages *[]struct {
		Type *string `json:"type"`
		User *string `json:"user"`
		Text *string `json:"text"`
	} `json:"messages"`
}

// parseSlackExport parses `{ "channel": {...}, "messages": [ { user, ts,
// text } ] }` — both full channel files and files.json-style single-message
// objects that wrap a `message` field. Messages are kept when typed
// "message" or when they carry text; the participant falls back to unknown.
//
// NOTE (parity): the Rust SlackMessage struct never deserializes `ts`, so
// every mined Slack timestamp is empty; this port keeps that shape. Extracted
// records are unaffected (classification and topics come from the text).
func parseSlackExport(content []byte) ([]RawMessage, error) {
	var export slackExport
	if err := json.Unmarshal(content, &export); err != nil {
		return nil, fmt.Errorf("not a Slack export: %w", err)
	}
	if export.Messages == nil {
		return nil, fmt.Errorf("not a Slack export: missing messages array")
	}
	var out []RawMessage
	for _, msg := range *export.Messages {
		if (msg.Type != nil && *msg.Type == "message") || msg.Text != nil {
			participant := "unknown"
			if msg.User != nil {
				participant = *msg.User
			}
			text := ""
			if msg.Text != nil {
				text = *msg.Text
			}
			out = append(out, RawMessage{
				Participant: participant,
				Text:        text,
				Source:      "slack",
			})
		}
	}
	return out, nil
}
