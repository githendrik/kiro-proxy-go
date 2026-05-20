package streaming

import (
	"strings"
	"testing"

	"kiro-proxy-go/internal/parser"
)

func TestStreamConverter_ContentEvent(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", false)

	event := parser.Event{
		Type:    "content",
		Content: "Hello",
	}

	chunks := sc.ProcessEvent(event)
	if len(chunks) == 0 {
		t.Fatal("Expected at least 1 chunk")
	}

	if !strings.Contains(chunks[0], "Hello") {
		t.Errorf("Expected chunk to contain 'Hello', got '%s'", chunks[0])
	}
}

func TestStreamConverter_ToolUseEvent(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", false)

	event := parser.Event{
		Type: "tool_use",
		ToolUse: &parser.ToolUseEvent{
			ToolUseID: "call123",
			Name:      "calculator",
			Input:     `{"expr":`,
		},
	}

	chunks := sc.ProcessEvent(event)
	if len(chunks) == 0 {
		t.Fatal("Expected at least 1 chunk")
	}

	if !strings.Contains(chunks[0], "calculator") {
		t.Errorf("Expected chunk to contain tool name, got '%s'", chunks[0])
	}

	if !strings.Contains(chunks[0], "call123") {
		t.Errorf("Expected chunk to contain tool ID, got '%s'", chunks[0])
	}
}

func TestStreamConverter_Finish(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", false)

	chunk := sc.Finish("")
	if !strings.Contains(chunk, "stop") {
		t.Errorf("Expected finish chunk to contain 'stop', got '%s'", chunk)
	}
}

func TestStreamConverter_FinishWithToolCalls(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", false)

	// Simulate a tool call
	event := parser.Event{
		Type: "tool_use",
		ToolUse: &parser.ToolUseEvent{
			ToolUseID: "call123",
			Name:      "calculator",
			Input:     `{"expr":"2+2"}`,
		},
	}
	sc.ProcessEvent(event)

	chunk := sc.Finish("")
	if !strings.Contains(chunk, "tool_calls") {
		t.Errorf("Expected finish chunk to contain 'tool_calls', got '%s'", chunk)
	}
}

func TestStreamConverter_ThinkingContent(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", true)

	event := parser.Event{
		Type:       "content",
		Content:    "<thinking>Let me think</thinking>Answer",
		IsThinking: true,
	}

	chunks := sc.ProcessEvent(event)
	if len(chunks) == 0 {
		t.Fatal("Expected at least 1 chunk")
	}
}

func TestStreamConverter_MultipleContentChunks(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", false)

	events := []parser.Event{
		{Type: "content", Content: "Hello"},
		{Type: "content", Content: " "},
		{Type: "content", Content: "World"},
	}

	var allChunks []string
	for _, event := range events {
		chunks := sc.ProcessEvent(event)
		allChunks = append(allChunks, chunks...)
	}

	if len(allChunks) == 0 {
		t.Fatal("Expected chunks to be generated")
	}
}
