package parser

import (
	"encoding/json"
	"fmt"
)

// Event represents a parsed event from the Kiro API stream.
type Event struct {
	Type           string // "content", "tool_use", "usage", "end", "error"
	Content        string
	ToolUse        *ToolUseEvent
	Usage          *UsageEvent
	Error          string
	IsThinking     bool
	ModelID        string
	ConversationID string
}

// ToolUseEvent represents a tool use event from the stream.
type ToolUseEvent struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
	Input     string // accumulated JSON string
}

// UsageEvent represents token usage information.
type UsageEvent struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// Parser handles Kiro API stream parsing by extracting JSON from binary AWS Event Stream.
type Parser struct {
	buffer string
}

// NewParser creates a new parser.
func NewParser() *Parser {
	return &Parser{}
}

// Feed adds data to the parser buffer and returns any complete events.
func (p *Parser) Feed(data []byte) ([]Event, error) {
	// Decode as UTF-8, ignoring invalid bytes (AWS Event Stream binary framing)
	p.buffer += string(data)

	var events []Event

	// Find JSON objects in the buffer
	for {
		// Find start of JSON object
		start := -1
		for i := 0; i < len(p.buffer); i++ {
			if p.buffer[i] == '{' {
				start = i
				break
			}
		}

		if start == -1 {
			// No JSON found, clear buffer
			p.buffer = ""
			break
		}

		// Find matching closing brace
		end := findMatchingBrace(p.buffer, start)
		if end == -1 {
			// Incomplete JSON, keep in buffer
			p.buffer = p.buffer[start:]
			break
		}

		// Extract and parse JSON
		jsonStr := p.buffer[start : end+1]
		event, err := parseJSONPayload([]byte(jsonStr))
		if err == nil && event != nil {
			events = append(events, *event)
		}

		// Remove processed JSON from buffer
		p.buffer = p.buffer[end+1:]
	}

	return events, nil
}

// findMatchingBrace finds the position of the matching closing brace.
func findMatchingBrace(text string, start int) int {
	if start >= len(text) || text[start] != '{' {
		return -1
	}

	braceCount := 0
	inString := false
	escapeNext := false

	for i := start; i < len(text); i++ {
		char := text[i]

		if escapeNext {
			escapeNext = false
			continue
		}

		if char == '\\' && inString {
			escapeNext = true
			continue
		}

		if char == '"' && !escapeNext {
			inString = !inString
			continue
		}

		if !inString {
			if char == '{' {
				braceCount++
			} else if char == '}' {
				braceCount--
				if braceCount == 0 {
					return i
				}
			}
		}
	}

	return -1
}

// kiroPayload is the JSON payload structure from Kiro API.
type kiroPayload struct {
	Content        string `json:"content"`
	ModelID        string `json:"modelId"`
	ConversationID string `json:"conversationId"`

	// Tool use
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
	Input     string `json:"input"`

	// Usage
	Usage *struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`

	// Thinking content
	ThinkingContent string `json:"thinkingContent"`

	// Error
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// parseJSONPayload converts a binary payload into an Event.
func parseJSONPayload(payload []byte) (*Event, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty payload")
	}

	var raw kiroPayload
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse payload JSON: %w", err)
	}

	event := &Event{}

	// Content event
	if raw.Content != "" {
		event.Type = "content"
		event.Content = raw.Content
		event.ModelID = raw.ModelID
		event.ConversationID = raw.ConversationID
	}

	// Tool use event
	if raw.ToolUseID != "" || raw.Name != "" || raw.Input != "" {
		event.Type = "tool_use"
		event.ToolUse = &ToolUseEvent{
			ToolUseID: raw.ToolUseID,
			Name:      raw.Name,
			Input:     raw.Input,
		}
	}

	// Thinking content event
	if raw.ThinkingContent != "" {
		event.Type = "content"
		event.Content = raw.ThinkingContent
		event.IsThinking = true
	}

	// Usage event
	if raw.Usage != nil {
		event.Type = "usage"
		event.Usage = &UsageEvent{
			InputTokens:  raw.Usage.InputTokens,
			OutputTokens: raw.Usage.OutputTokens,
		}
	}

	// Error event
	if raw.Error != nil {
		event.Type = "error"
		event.Error = raw.Error.Message
	}

	// Check if this is a completion marker
	if raw.ConversationID != "" && raw.Content == "" && raw.ToolUseID == "" && raw.Usage == nil {
		event.Type = "end"
	}

	return event, nil
}
