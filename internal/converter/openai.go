package converter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// OpenAI request types

// ChatCompletionRequest is the OpenAI-compatible request format.
type ChatCompletionRequest struct {
	Model       string          `json:"model"`
	Messages    []Message       `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Tools       []Tool          `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
}

// Message represents a chat message in OpenAI format.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"` // string or []ContentPart
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// ContentPart represents a part of a multi-part message content.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL holds image data for vision requests.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// Tool represents a tool definition in OpenAI format.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function definition within a tool.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall represents a tool call in a message.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the function call details.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Kiro payload types

// KiroPayload is the top-level request to generateAssistantResponse.
type KiroPayload struct {
	ConversationState KiroConversationState `json:"conversationState"`
	ProfileARN        string                `json:"profileArn,omitempty"`
}

// KiroConversationState holds the conversation context.
type KiroConversationState struct {
	ChatTriggerType string             `json:"chatTriggerType"`
	ConversationID  string             `json:"conversationId"`
	CurrentMessage  KiroCurrentMessage `json:"currentMessage"`
	History         []KiroHistoryEntry `json:"history,omitempty"`
}

// KiroCurrentMessage wraps the current user input.
type KiroCurrentMessage struct {
	UserInputMessage KiroUserInputMessage `json:"userInputMessage"`
}

// KiroUserInputMessage is a user message in Kiro format.
type KiroUserInputMessage struct {
	Content                 string                   `json:"content"`
	ModelID                 string                   `json:"modelId"`
	Origin                  string                   `json:"origin"`
	UserInputMessageContext *KiroUserInputMsgContext `json:"userInputMessageContext,omitempty"`
}

// KiroUserInputMsgContext provides tools and tool results.
type KiroUserInputMsgContext struct {
	Tools       []KiroToolSpec   `json:"tools,omitempty"`
	ToolResults []KiroToolResult `json:"toolResults,omitempty"`
}

// KiroToolSpec is a tool specification in Kiro format.
type KiroToolSpec struct {
	ToolSpecification KiroToolSpecification `json:"toolSpecification"`
}

// KiroToolSpecification is the inner tool definition.
type KiroToolSpecification struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	InputSchema *KiroToolInputSchema `json:"inputSchema,omitempty"`
}

// KiroToolInputSchema wraps the JSON schema for tool input.
type KiroToolInputSchema struct {
	JSON json.RawMessage `json:"json"`
}

// KiroToolResult is a tool result in Kiro format.
type KiroToolResult struct {
	Content   []KiroToolResultContent `json:"content"`
	Status    string                  `json:"status"`
	ToolUseID string                  `json:"toolUseId"`
}

// KiroToolResultContent is the content of a tool result.
type KiroToolResultContent struct {
	Text string `json:"text,omitempty"`
}

// KiroHistoryEntry is a single entry in the conversation history.
// Either UserInputMessage or AssistantResponseMessage is set.
type KiroHistoryEntry struct {
	UserInputMessage         *KiroUserInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *KiroAssistantResponseMessage `json:"assistantResponseMessage,omitempty"`
}

// KiroAssistantResponseMessage is an assistant message in Kiro format.
type KiroAssistantResponseMessage struct {
	Content  string         `json:"content"`
	ToolUses []KiroToolUse  `json:"toolUses,omitempty"`
}

// KiroToolUse is a tool use in Kiro format.
type KiroToolUse struct {
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"toolUseId"`
}

// BuildKiroPayload converts an OpenAI ChatCompletionRequest to a Kiro API payload.
func BuildKiroPayload(req *ChatCompletionRequest, profileARN string, fakeReasoning bool, thinkingBudget int) (*KiroPayload, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("messages array is empty")
	}

	conversationID := uuid.New().String()

	// Extract system prompt
	systemPrompt, messages := extractSystemPrompt(req.Messages)

	// Inject thinking tags if fake reasoning is enabled
	if fakeReasoning {
		thinkingTag := fmt.Sprintf("<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>%d</max_thinking_length>", thinkingBudget)
		if systemPrompt != "" {
			systemPrompt = thinkingTag + "\n\n" + systemPrompt
		} else {
			systemPrompt = thinkingTag
		}
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("no messages after system prompt extraction")
	}

	// Convert tools to Kiro format
	var kiroTools []KiroToolSpec
	for _, tool := range req.Tools {
		spec := KiroToolSpec{
			ToolSpecification: KiroToolSpecification{
				Name:        sanitizeToolName(tool.Function.Name),
				Description: tool.Function.Description,
			},
		}
		if len(tool.Function.Parameters) > 0 {
			sanitized := sanitizeJSONSchema(tool.Function.Parameters)
			spec.ToolSpecification.InputSchema = &KiroToolInputSchema{JSON: sanitized}
		}
		kiroTools = append(kiroTools, spec)
	}

	// Build history and current message
	history, currentContent, currentToolResults := buildHistory(messages, systemPrompt, req.Model)

	// Build current message
	currentMsg := KiroUserInputMessage{
		Content: currentContent,
		ModelID: req.Model,
		Origin:  "AI_EDITOR",
	}

	// Attach tools and tool results to current message
	if len(kiroTools) > 0 || len(currentToolResults) > 0 {
		currentMsg.UserInputMessageContext = &KiroUserInputMsgContext{
			Tools:       kiroTools,
			ToolResults: currentToolResults,
		}
	}

	payload := &KiroPayload{
		ConversationState: KiroConversationState{
			ChatTriggerType: "MANUAL",
			ConversationID:  conversationID,
			CurrentMessage:  KiroCurrentMessage{UserInputMessage: currentMsg},
			History:         history,
		},
		ProfileARN: profileARN,
	}

	return payload, nil
}

// extractSystemPrompt pulls out system messages from the message list.
func extractSystemPrompt(messages []Message) (string, []Message) {
	var systemParts []string
	var remaining []Message

	for _, msg := range messages {
		if msg.Role == "system" {
			text := extractTextContent(msg.Content)
			if text != "" {
				systemParts = append(systemParts, text)
			}
		} else {
			remaining = append(remaining, msg)
		}
	}

	return strings.Join(systemParts, "\n\n"), remaining
}

// extractTextContent gets the text from a message content field
// (handles both string and array formats).
func extractTextContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	// Try as string first
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}

	// Try as array of content parts
	var parts []ContentPart
	if err := json.Unmarshal(content, &parts); err == nil {
		var texts []string
		for _, p := range parts {
			if p.Type == "text" {
				texts = append(texts, p.Text)
			}
		}
		return strings.Join(texts, "\n")
	}

	return string(content)
}

// buildHistory converts OpenAI messages into Kiro history + current message content.
// Returns: history entries, current message content, current tool results.
func buildHistory(messages []Message, systemPrompt, modelID string) ([]KiroHistoryEntry, string, []KiroToolResult) {
	// Separate trailing tool messages (they become tool results on current message)
	mainMessages, trailingToolMsgs := splitTrailingToolMessages(messages)

	var history []KiroHistoryEntry
	var currentContent string

	// If we have trailing tool messages, ALL main messages go into history
	// and the current message is just "Continue" with tool results
	if len(trailingToolMsgs) > 0 {
		for _, msg := range mainMessages {
			entry := convertToHistoryEntry(msg, modelID)
			if entry != nil {
				history = append(history, *entry)
			}
		}
		currentContent = "Continue"
	} else {
		// No tool results: last message becomes current, rest go into history
		if len(mainMessages) == 0 {
			currentContent = "(empty)"
		} else {
			lastIdx := len(mainMessages) - 1
			for i := 0; i < lastIdx; i++ {
				msg := mainMessages[i]
				entry := convertToHistoryEntry(msg, modelID)
				if entry != nil {
					history = append(history, *entry)
				}
			}
			lastMsg := mainMessages[lastIdx]
			currentContent = extractTextContent(lastMsg.Content)
			if currentContent == "" {
				currentContent = "(empty placeholder)"
			}
		}
	}

	// Prepend system prompt to first user message in history, or to current
	if systemPrompt != "" {
		if len(history) > 0 && history[0].UserInputMessage != nil {
			history[0].UserInputMessage.Content = systemPrompt + "\n\n" + history[0].UserInputMessage.Content
		} else {
			currentContent = systemPrompt + "\n\n" + currentContent
		}
	}

	// Convert trailing tool messages to tool results
	var toolResults []KiroToolResult
	for _, msg := range trailingToolMsgs {
		text := extractTextContent(msg.Content)
		toolResults = append(toolResults, KiroToolResult{
			Content:   []KiroToolResultContent{{Text: text}},
			Status:    "success",
			ToolUseID: msg.ToolCallID,
		})
	}

	return history, currentContent, toolResults
}

// splitTrailingToolMessages separates trailing tool messages from the rest.
func splitTrailingToolMessages(messages []Message) ([]Message, []Message) {
	// Find where trailing tool messages start
	toolStart := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			toolStart = i
		} else {
			break
		}
	}

	return messages[:toolStart], messages[toolStart:]
}

// convertToHistoryEntry converts a single OpenAI message to a Kiro history entry.
func convertToHistoryEntry(msg Message, modelID string) *KiroHistoryEntry {
	switch msg.Role {
	case "user":
		content := extractTextContent(msg.Content)
		if content == "" {
			content = "(empty placeholder)"
		}
		return &KiroHistoryEntry{
			UserInputMessage: &KiroUserInputMessage{
				Content: content,
				ModelID: modelID,
				Origin:  "AI_EDITOR",
			},
		}

	case "assistant":
		content := extractTextContent(msg.Content)
		// Kiro API requires non-empty content - use "(empty)" placeholder
		if content == "" {
			content = "(empty)"
		}
		var toolUses []KiroToolUse
		for _, tc := range msg.ToolCalls {
			toolUses = append(toolUses, KiroToolUse{
				Name:      tc.Function.Name,
				Input:     json.RawMessage(tc.Function.Arguments),
				ToolUseID: tc.ID,
			})
		}
		return &KiroHistoryEntry{
			AssistantResponseMessage: &KiroAssistantResponseMessage{
				Content:  content,
				ToolUses: toolUses,
			},
		}

	case "tool":
		// Tool messages should not create separate history entries.
		// They are handled as trailing messages and become toolResults in the current message.
		// If a tool message appears in the middle of history, skip it.
		return nil

	default:
		return nil
	}
}

// sanitizeToolName ensures tool names are <= 64 chars and valid.
func sanitizeToolName(name string) string {
	if len(name) > 64 {
		return name[:64]
	}
	return name
}

// sanitizeJSONSchema removes unsupported fields from JSON schemas.
func sanitizeJSONSchema(schema json.RawMessage) json.RawMessage {
	var m map[string]interface{}
	if err := json.Unmarshal(schema, &m); err != nil {
		return schema
	}

	// Remove additionalProperties (Kiro doesn't support it)
	delete(m, "additionalProperties")

	// Remove empty required arrays
	if req, ok := m["required"]; ok {
		if arr, ok := req.([]interface{}); ok && len(arr) == 0 {
			delete(m, "required")
		}
	}

	// Recursively sanitize properties
	if props, ok := m["properties"].(map[string]interface{}); ok {
		for key, val := range props {
			if propMap, ok := val.(map[string]interface{}); ok {
				propBytes, _ := json.Marshal(propMap)
				sanitized := sanitizeJSONSchema(propBytes)
				var sanitizedMap map[string]interface{}
				json.Unmarshal(sanitized, &sanitizedMap)
				props[key] = sanitizedMap
			}
		}
	}

	result, _ := json.Marshal(m)
	return result
}
