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

// unifiedMessage is an intermediate representation used during conversion.
// Tool messages are converted into user messages with toolResults attached.
type unifiedMessage struct {
	Role        string
	Content     string
	ToolCalls   []ToolCall
	ToolResults []KiroToolResult
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

	// Step 1: Convert OpenAI messages to unified format (tool messages become user messages with toolResults)
	unified := convertToUnified(messages)

	// Step 2: Merge adjacent same-role messages
	unified = mergeAdjacentMessages(unified)

	// Step 3: Ensure first message is user
	if len(unified) > 0 && unified[0].Role != "user" {
		unified = append([]unifiedMessage{{Role: "user", Content: "(empty)"}}, unified...)
	}

	// Step 4: Ensure alternating user/assistant roles
	unified = ensureAlternatingRoles(unified)

	// Step 5: Split into history + currentMessage
	history, currentContent, currentToolResults := splitHistoryAndCurrent(unified, systemPrompt, req.Model)

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

// convertToUnified converts OpenAI messages to unified format.
// Tool messages are accumulated and flushed as user messages with toolResults.
func convertToUnified(messages []Message) []unifiedMessage {
	var result []unifiedMessage
	var pendingToolResults []KiroToolResult

	for _, msg := range messages {
		switch msg.Role {
		case "tool":
			// Accumulate tool results
			text := extractTextContent(msg.Content)
			if text == "" {
				text = "(empty result)"
			}
			pendingToolResults = append(pendingToolResults, KiroToolResult{
				Content:   []KiroToolResultContent{{Text: text}},
				Status:    "success",
				ToolUseID: msg.ToolCallID,
			})

		default:
			// Flush pending tool results as a user message before this message
			if len(pendingToolResults) > 0 {
				result = append(result, unifiedMessage{
					Role:        "user",
					Content:     "",
					ToolResults: pendingToolResults,
				})
				pendingToolResults = nil
			}

			// Convert the current message
			content := extractTextContent(msg.Content)
			role := msg.Role
			// Normalize unknown roles to "user"
			if role != "user" && role != "assistant" {
				role = "user"
			}

			um := unifiedMessage{
				Role:    role,
				Content: content,
			}

			if role == "assistant" && len(msg.ToolCalls) > 0 {
				um.ToolCalls = msg.ToolCalls
			}

			result = append(result, um)
		}
	}

	// Flush any remaining pending tool results
	if len(pendingToolResults) > 0 {
		result = append(result, unifiedMessage{
			Role:        "user",
			Content:     "",
			ToolResults: pendingToolResults,
		})
	}

	return result
}

// mergeAdjacentMessages merges consecutive messages with the same role.
func mergeAdjacentMessages(messages []unifiedMessage) []unifiedMessage {
	if len(messages) == 0 {
		return messages
	}

	var merged []unifiedMessage
	merged = append(merged, messages[0])

	for i := 1; i < len(messages); i++ {
		last := &merged[len(merged)-1]
		curr := messages[i]

		if last.Role == curr.Role {
			// Merge content
			if last.Content != "" && curr.Content != "" {
				last.Content = last.Content + "\n\n" + curr.Content
			} else if curr.Content != "" {
				last.Content = curr.Content
			}
			// Merge tool calls
			last.ToolCalls = append(last.ToolCalls, curr.ToolCalls...)
			// Merge tool results
			last.ToolResults = append(last.ToolResults, curr.ToolResults...)
		} else {
			merged = append(merged, curr)
		}
	}

	return merged
}

// ensureAlternatingRoles inserts synthetic messages to maintain strict user/assistant alternation.
func ensureAlternatingRoles(messages []unifiedMessage) []unifiedMessage {
	if len(messages) == 0 {
		return messages
	}

	var result []unifiedMessage
	result = append(result, messages[0])

	for i := 1; i < len(messages); i++ {
		prev := result[len(result)-1]
		curr := messages[i]

		if prev.Role == curr.Role {
			// Insert a synthetic message of the opposite role
			if prev.Role == "user" {
				result = append(result, unifiedMessage{Role: "assistant", Content: "(empty)"})
			} else {
				result = append(result, unifiedMessage{Role: "user", Content: "(empty)"})
			}
		}

		result = append(result, curr)
	}

	return result
}

// splitHistoryAndCurrent splits unified messages into Kiro history entries and current message.
func splitHistoryAndCurrent(messages []unifiedMessage, systemPrompt, modelID string) ([]KiroHistoryEntry, string, []KiroToolResult) {
	if len(messages) == 0 {
		content := "(empty)"
		if systemPrompt != "" {
			content = systemPrompt + "\n\n" + content
		}
		return nil, content, nil
	}

	var historyMessages []unifiedMessage
	var currentContent string
	var currentToolResults []KiroToolResult

	lastMsg := messages[len(messages)-1]

	if lastMsg.Role == "assistant" {
		// If last message is assistant, push ALL messages to history, currentMessage = "Continue"
		historyMessages = messages
		currentContent = "Continue"
	} else {
		// Last message is user: it becomes currentMessage, rest go to history
		historyMessages = messages[:len(messages)-1]
		currentContent = lastMsg.Content
		if currentContent == "" {
			currentContent = "Continue"
		}
		currentToolResults = lastMsg.ToolResults
	}

	// Prepend system prompt to first user message in history, or to current
	systemApplied := false
	if systemPrompt != "" {
		for i := range historyMessages {
			if historyMessages[i].Role == "user" {
				historyMessages[i].Content = systemPrompt + "\n\n" + historyMessages[i].Content
				systemApplied = true
				break
			}
		}
		if !systemApplied {
			currentContent = systemPrompt + "\n\n" + currentContent
		}
	}

	// Convert history messages to Kiro format
	var history []KiroHistoryEntry
	for _, msg := range historyMessages {
		switch msg.Role {
		case "user":
			content := msg.Content
			if content == "" {
				content = "(empty placeholder)"
			}
			entry := KiroHistoryEntry{
				UserInputMessage: &KiroUserInputMessage{
					Content: content,
					ModelID: modelID,
					Origin:  "AI_EDITOR",
				},
			}
			// Attach tool results to user messages in history
			if len(msg.ToolResults) > 0 {
				entry.UserInputMessage.UserInputMessageContext = &KiroUserInputMsgContext{
					ToolResults: msg.ToolResults,
				}
			}
			history = append(history, entry)

		case "assistant":
			content := msg.Content
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
			history = append(history, KiroHistoryEntry{
				AssistantResponseMessage: &KiroAssistantResponseMessage{
					Content:  content,
					ToolUses: toolUses,
				},
			})
		}
	}

	return history, currentContent, currentToolResults
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
