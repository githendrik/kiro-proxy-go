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

	// When last message is assistant, all messages go to history and currentMessage = "Continue"
	if len(payload.ConversationState.History) != 2 {
		t.Fatalf("Expected 2 history entries, got %d", len(payload.ConversationState.History))
	}

	// First history entry should be user message
	if payload.ConversationState.History[0].UserInputMessage == nil {
		t.Fatal("Expected UserInputMessage in first history entry")
	}

	// Second history entry should be assistant
	// Note: tool calls are converted to text when no tools are defined (Kiro API requirement)
	if payload.ConversationState.History[1].AssistantResponseMessage == nil {
		t.Fatal("Expected AssistantResponseMessage in second history entry")
	}

	// Tool call should be converted to text in content
	content := payload.ConversationState.History[1].AssistantResponseMessage.Content
	if !contains(content, "[Tool: calculator (call123)]") {
		t.Errorf("Expected tool call converted to text, got '%s'", content)
	}

	// Current message should be "Continue" since last message was assistant
	if payload.ConversationState.CurrentMessage.UserInputMessage.Content != "Continue" {
		t.Errorf("Expected content 'Continue', got '%s'", payload.ConversationState.CurrentMessage.UserInputMessage.Content)
	}
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

func TestBuildKiroPayload_MultiTurnToolUse(t *testing.T) {
	// This simulates a subagent/tool-use conversation with multiple rounds:
	// user -> assistant(tool_call) -> tool(result) -> assistant(tool_call) -> tool(result) -> assistant(tool_call) -> tool(result)
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"Help me plan a feature"`)},
			{
				Role:    "assistant",
				Content: json.RawMessage(`"Let me search the codebase."`),
				ToolCalls: []ToolCall{
					{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "grep", Arguments: `{"pattern": "pipeline"}`}},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: json.RawMessage(`"Found 3 matches in src/"`)},
			{
				Role:    "assistant",
				Content: json.RawMessage(`"Let me read the file."`),
				ToolCalls: []ToolCall{
					{ID: "call_2", Type: "function", Function: ToolCallFunction{Name: "read", Arguments: `{"path": "src/pipeline.go"}`}},
				},
			},
			{Role: "tool", ToolCallID: "call_2", Content: json.RawMessage(`"package main\nfunc runPipeline() {}"`)},
			{
				Role:    "assistant",
				Content: json.RawMessage(`"Now let me check the config."`),
				ToolCalls: []ToolCall{
					{ID: "call_3", Type: "function", Function: ToolCallFunction{Name: "read", Arguments: `{"path": ".gitlab-ci.yml"}`}},
				},
			},
			{Role: "tool", ToolCallID: "call_3", Content: json.RawMessage(`"stages:\n  - build\n  - deploy"`)},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "grep", Description: "Search files", Parameters: json.RawMessage(`{"type":"object"}`)}},
			{Type: "function", Function: ToolFunction{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)}},
		},
	}

	payload, err := BuildKiroPayload(req, "", false, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	// Verify history has strict alternating user/assistant pattern
	history := payload.ConversationState.History
	for i, entry := range history {
		if i%2 == 0 {
			if entry.UserInputMessage == nil {
				t.Errorf("History[%d]: expected UserInputMessage, got AssistantResponseMessage", i)
			}
		} else {
			if entry.AssistantResponseMessage == nil {
				t.Errorf("History[%d]: expected AssistantResponseMessage, got UserInputMessage", i)
			}
		}
	}

	// Verify tool results are attached to user messages (not dropped)
	// Pattern should be: user, assistant(tool_call), user(tool_result), assistant(tool_call), user(tool_result), assistant(tool_call)
	// Current message should be user with tool_result for call_3
	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if ctx == nil {
		t.Fatal("Expected UserInputMessageContext on current message")
	}
	if len(ctx.ToolResults) != 1 {
		t.Fatalf("Expected 1 tool result on current message, got %d", len(ctx.ToolResults))
	}
	if ctx.ToolResults[0].ToolUseID != "call_3" {
		t.Errorf("Expected tool result for call_3, got %s", ctx.ToolResults[0].ToolUseID)
	}

	// Current content should be "Continue"
	if payload.ConversationState.CurrentMessage.UserInputMessage.Content != "Continue" {
		t.Errorf("Expected current content 'Continue', got '%s'", payload.ConversationState.CurrentMessage.UserInputMessage.Content)
	}

	// Verify no nil entries in history
	for i, entry := range history {
		if entry.UserInputMessage == nil && entry.AssistantResponseMessage == nil {
			t.Errorf("History[%d]: both UserInputMessage and AssistantResponseMessage are nil", i)
		}
	}
}

func TestBuildKiroPayload_MidConversationToolResults(t *testing.T) {
	// Simulates: user -> assistant(tool) -> tool_result -> user (follow-up question)
	// The tool result should appear in history as a user message with toolResults, not be dropped
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4",
		Messages: []Message{
			{Role: "user", Content: json.RawMessage(`"Search for errors"`)},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "grep", Arguments: `{"pattern": "error"}`}},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: json.RawMessage(`"error.go:10: handle error"`)},
			{Role: "assistant", Content: json.RawMessage(`"I found an error in error.go line 10."`)},
			{Role: "user", Content: json.RawMessage(`"Fix it please"`)},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "grep", Description: "Search", Parameters: json.RawMessage(`{"type":"object"}`)}},
		},
	}

	payload, err := BuildKiroPayload(req, "", false, 4000)
	if err != nil {
		t.Fatalf("BuildKiroPayload failed: %v", err)
	}

	// Current message should be the last user message
	if payload.ConversationState.CurrentMessage.UserInputMessage.Content != "Fix it please" {
		t.Errorf("Expected current content 'Fix it please', got '%s'", payload.ConversationState.CurrentMessage.UserInputMessage.Content)
	}

	// History should have: user, assistant(tool_call), user(tool_result), assistant
	history := payload.ConversationState.History
	if len(history) != 4 {
		t.Fatalf("Expected 4 history entries, got %d", len(history))
	}

	// Verify alternation
	if history[0].UserInputMessage == nil {
		t.Error("History[0] should be user")
	}
	if history[1].AssistantResponseMessage == nil {
		t.Error("History[1] should be assistant")
	}
	if history[2].UserInputMessage == nil {
		t.Error("History[2] should be user (tool result)")
	}
	if history[3].AssistantResponseMessage == nil {
		t.Error("History[3] should be assistant")
	}

	// Verify tool result is attached to the user message in history
	if history[2].UserInputMessage.UserInputMessageContext == nil {
		t.Fatal("History[2] should have UserInputMessageContext with tool results")
	}
	if len(history[2].UserInputMessage.UserInputMessageContext.ToolResults) != 1 {
		t.Fatalf("Expected 1 tool result in history[2], got %d", len(history[2].UserInputMessage.UserInputMessageContext.ToolResults))
	}
	if history[2].UserInputMessage.UserInputMessageContext.ToolResults[0].ToolUseID != "call_1" {
		t.Errorf("Expected toolUseId 'call_1', got '%s'", history[2].UserInputMessage.UserInputMessageContext.ToolResults[0].ToolUseID)
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
