package converter

import (
	"encoding/json"
	"testing"
)

func TestBuildKiroPayload_SimpleMessage(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
		},
	}

	payload, err := BuildKiroPayload(req, "", false, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	if payload.ConversationState.CurrentMessage.UserInputMessage.Content != "Hello" {
		t.Errorf("Expected content 'Hello', got '%s'", payload.ConversationState.CurrentMessage.UserInputMessage.Content)
	}

	if len(payload.ConversationState.History) != 0 {
		t.Errorf("Expected empty history, got %d entries", len(payload.ConversationState.History))
	}
}

func TestBuildKiroPayload_SystemPrompt(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "system", Content: json.RawMessage(`"You are a helpful assistant"`)},
			{Role: "user", Content: json.RawMessage(`"Hi"`)},
		},
	}

	payload, err := BuildKiroPayload(req, "", false, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	content := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if content != "You are a helpful assistant\n\nHi" {
		t.Errorf("Expected system prompt prepended, got '%s'", content)
	}
}

func TestBuildKiroPayload_WithTools(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"Calculate 2+2"`)},
		},
		Tools: []Tool{
			{
				Type: "function",
				Function: ToolFunction{
					Name:        "calculator",
					Description: "Perform calculations",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"expr":{"type":"string"}},"required":["expr"]}`),
				},
			},
		},
	}

	payload, err := BuildKiroPayload(req, "", false, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if ctx == nil {
		t.Fatal("Expected UserInputMessageContext to be set")
	}

	if len(ctx.Tools) != 1 {
		t.Errorf("Expected 1 tool, got %d", len(ctx.Tools))
	}

	if ctx.Tools[0].ToolSpecification.Name != "calculator" {
		t.Errorf("Expected tool name 'calculator', got '%s'", ctx.Tools[0].ToolSpecification.Name)
	}
}

func TestBuildKiroPayload_ToolCall(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"What is 2+2?"`)},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{
						ID:   "call123",
						Type: "function",
						Function: ToolCallFunction{
							Name:      "calculator",
							Arguments: `{"expr": "2+2"}`,
						},
					},
				},
			},
		},
	}

	payload, err := BuildKiroPayload(req, "", false, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	if len(payload.ConversationState.History) != 1 {
		t.Fatalf("Expected 1 history entry, got %d", len(payload.ConversationState.History))
	}

	historyEntry := payload.ConversationState.History[0]
	if historyEntry.UserInputMessage == nil {
		t.Fatal("Expected UserInputMessage in history")
	}

	if historyEntry.AssistantResponseMessage != nil {
		t.Fatal("Did not expect AssistantResponseMessage in first history entry")
	}

	if len(payload.ConversationState.History) != 1 {
		t.Fatalf("Expected 1 history entry, got %d", len(payload.ConversationState.History))
	}

	// Second message (assistant with tool call) should be in current message or history
	// Actually with our implementation, last user message is current, assistant goes to history
}

func TestBuildKiroPayload_ToolResults(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"What is 2+2?"`)},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{
						ID:   "call123",
						Type: "function",
						Function: ToolCallFunction{
							Name:      "calculator",
							Arguments: `{"expr": "2+2"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call123",
				Content:    json.RawMessage(`"4"`),
			},
		},
		Tools: []Tool{
			{
				Type: "function",
				Function: ToolFunction{
					Name:        "calculator",
					Description: "Perform calculations",
					Parameters:  json.RawMessage(`{"type":"object"}`),
				},
			},
		},
	}

	payload, err := BuildKiroPayload(req, "", false, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	// Verify history has user and assistant messages
	if len(payload.ConversationState.History) != 2 {
		t.Fatalf("Expected 2 history entries, got %d", len(payload.ConversationState.History))
	}

	// First history entry should be user message
	if payload.ConversationState.History[0].UserInputMessage == nil {
		t.Fatal("Expected UserInputMessage in first history entry")
	}

	// Second history entry should be assistant with tool calls
	if payload.ConversationState.History[1].AssistantResponseMessage == nil {
		t.Fatal("Expected AssistantResponseMessage in second history entry")
	}

	if len(payload.ConversationState.History[1].AssistantResponseMessage.ToolUses) != 1 {
		t.Errorf("Expected 1 tool use, got %d", len(payload.ConversationState.History[1].AssistantResponseMessage.ToolUses))
	}

	// Current message should have tool results
	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if ctx == nil {
		t.Fatal("Expected UserInputMessageContext to be set")
	}

	if len(ctx.ToolResults) != 1 {
		t.Errorf("Expected 1 tool result, got %d", len(ctx.ToolResults))
	}

	if ctx.ToolResults[0].ToolUseID != "call123" {
		t.Errorf("Expected toolUseID 'call123', got '%s'", ctx.ToolResults[0].ToolUseID)
	}

	if ctx.ToolResults[0].Content[0].Text != "4" {
		t.Errorf("Expected content '4', got '%s'", ctx.ToolResults[0].Content[0].Text)
	}

	// Current content should be "Continue"
	if payload.ConversationState.CurrentMessage.UserInputMessage.Content != "Continue" {
		t.Errorf("Expected content 'Continue', got '%s'", payload.ConversationState.CurrentMessage.UserInputMessage.Content)
	}

	// Tools should also be included
	if len(ctx.Tools) != 1 {
		t.Errorf("Expected 1 tool in context, got %d", len(ctx.Tools))
	}
}

func TestBuildKiroPayload_FakeReasoning(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
		},
	}

	payload, err := BuildKiroPayload(req, "", true, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	content := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if !contains(content, "<thinking_mode>") {
		t.Errorf("Expected thinking tags in content, got '%s'", content)
	}
}

func TestBuildKiroPayload_EmptyMessages(t *testing.T) {
	req := &ChatCompletionRequest{
		Model:    "claude-sonnet-4",
		Messages: []Message{},
	}

	_, err := BuildKiroPayload(req, "", false, 4000)
	if err == nil {
		t.Fatal("Expected error for empty messages")
	}
}

func TestBuildKiroPayload_MultipleMessages(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"First"`)},
			{Role: "assistant", Content: json.RawMessage(`"Response"`)},
			{Role: "user", Content: json.RawMessage(`"Second"`)},
		},
	}

	payload, err := BuildKiroPayload(req, "", false, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	if len(payload.ConversationState.History) != 2 {
		t.Errorf("Expected 2 history entries, got %d", len(payload.ConversationState.History))
	}

	if payload.ConversationState.CurrentMessage.UserInputMessage.Content != "Second" {
		t.Errorf("Expected current content 'Second', got '%s'", payload.ConversationState.CurrentMessage.UserInputMessage.Content)
	}
}

func TestSanitizeToolName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"calculator", "calculator"},
		{"my-tool", "my-tool"},
		{"tool.test", "tool.test"},
		{"tool@test", "tool@test"},
	}

	for _, tt := range tests {
		result := sanitizeToolName(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeToolName(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestExtractTextContent(t *testing.T) {
	tests := []struct {
		name     string
		content  json.RawMessage
		expected string
	}{
		{"string", json.RawMessage(`"hello"`), "hello"},
		{"empty string", json.RawMessage(`""`), ""},
		{"array", json.RawMessage(`[{"type":"text","text":"hello"}]`), "hello"},
		{"multiple texts", json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`), "a\nb"},
		{"null", json.RawMessage(`null`), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTextContent(tt.content)
			if result != tt.expected {
				t.Errorf("extractTextContent(%q) = %q, want %q", tt.content, result, tt.expected)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
