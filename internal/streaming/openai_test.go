package streaming

import (
	"strings"
	"testing"

	"kiro-proxy/internal/parser"
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

func TestStreamConverter_LateThinkingEventSuppressed(t *testing.T) {
	// Simulate the scenario where native thinking events arrive AFTER
	// regular content has already been emitted. These should be suppressed
	// to prevent misordered thinking appearing after the answer.
	sc := NewStreamConverter("claude-sonnet-4", "test-id", false)

	// First, emit some regular content
	contentEvent := parser.Event{
		Type:    "content",
		Content: "Here is the answer.",
	}
	chunks := sc.ProcessEvent(contentEvent)
	if len(chunks) == 0 {
		t.Fatal("Expected content chunk to be emitted")
	}
	if !strings.Contains(chunks[0], "Here is the answer.") {
		t.Errorf("Expected content in chunk, got '%s'", chunks[0])
	}

	// Now a late-arriving thinking event should be suppressed
	thinkingEvent := parser.Event{
		Type:       "content",
		Content:    "Let me reconsider...",
		IsThinking: true,
	}
	lateChunks := sc.ProcessEvent(thinkingEvent)
	if len(lateChunks) != 0 {
		t.Errorf("Expected late thinking event to be suppressed, got %d chunks", len(lateChunks))
	}
}

func TestStreamConverter_ThinkingBeforeContentAllowed(t *testing.T) {
	// Native thinking events that arrive BEFORE any content should be emitted normally.
	sc := NewStreamConverter("claude-sonnet-4", "test-id", false)

	// Thinking event arrives first
	thinkingEvent := parser.Event{
		Type:       "content",
		Content:    "Let me think about this...",
		IsThinking: true,
	}
	chunks := sc.ProcessEvent(thinkingEvent)
	if len(chunks) == 0 {
		t.Fatal("Expected thinking chunk to be emitted")
	}
	if !strings.Contains(chunks[0], "reasoning_content") {
		t.Errorf("Expected reasoning_content in chunk, got '%s'", chunks[0])
	}

	// Then regular content arrives
	contentEvent := parser.Event{
		Type:    "content",
		Content: "The answer is 42.",
	}
	contentChunks := sc.ProcessEvent(contentEvent)
	if len(contentChunks) == 0 {
		t.Fatal("Expected content chunk to be emitted")
	}
	if !strings.Contains(contentChunks[0], "The answer is 42.") {
		t.Errorf("Expected content in chunk, got '%s'", contentChunks[0])
	}
}

func TestStreamConverter_FlushBufferSuppressedAfterContent(t *testing.T) {
	// If the stream ends with an unclosed thinking block but content was
	// already sent, FlushBuffer should suppress the buffered thinking.
	sc := NewStreamConverter("claude-sonnet-4", "test-id", true)

	// Emit regular content first (bypassing fake reasoning)
	sc.sentContent = true
	sc.thinkingState = ThinkingActive
	sc.matchedTag = &supportedThinkingTags[0]
	sc.contentBuffer.WriteString("some leftover thinking")

	chunks := sc.FlushBuffer()
	if len(chunks) != 0 {
		t.Errorf("Expected FlushBuffer to suppress thinking after content, got %d chunks", len(chunks))
	}
}

func TestStreamConverter_ThinkTagVariant(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", true)

	event := parser.Event{
		Type:    "content",
		Content: "<think>Let me reason</think>The answer is 42.",
	}

	chunks := sc.ProcessEvent(event)
	if len(chunks) == 0 {
		t.Fatal("Expected chunks to be generated")
	}

	// Should have reasoning_content and content chunks
	hasReasoning := false
	hasContent := false
	for _, chunk := range chunks {
		if strings.Contains(chunk, "reasoning_content") && strings.Contains(chunk, "Let me reason") {
			hasReasoning = true
		}
		if strings.Contains(chunk, `"content"`) && strings.Contains(chunk, "The answer is 42.") {
			hasContent = true
		}
	}
	if !hasReasoning {
		t.Error("Expected reasoning_content chunk with 'Let me reason'")
	}
	if !hasContent {
		t.Error("Expected content chunk with 'The answer is 42.'")
	}
}

func TestStreamConverter_ThoughtTagVariant(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", true)

	event := parser.Event{
		Type:    "content",
		Content: "<thought>Deep thought here</thought>Result.",
	}

	chunks := sc.ProcessEvent(event)
	if len(chunks) == 0 {
		t.Fatal("Expected chunks to be generated")
	}

	hasReasoning := false
	hasContent := false
	for _, chunk := range chunks {
		if strings.Contains(chunk, "reasoning_content") && strings.Contains(chunk, "Deep thought here") {
			hasReasoning = true
		}
		if strings.Contains(chunk, `"content"`) && strings.Contains(chunk, "Result.") {
			hasContent = true
		}
	}
	if !hasReasoning {
		t.Error("Expected reasoning_content chunk with 'Deep thought here'")
	}
	if !hasContent {
		t.Error("Expected content chunk with 'Result.'")
	}
}

func TestStreamConverter_ReasoningTagVariant(t *testing.T) {
	sc := NewStreamConverter("claude-sonnet-4", "test-id", true)

	event := parser.Event{
		Type:    "content",
		Content: "<reasoning>Step by step</reasoning>Final answer.",
	}

	chunks := sc.ProcessEvent(event)
	if len(chunks) == 0 {
		t.Fatal("Expected chunks to be generated")
	}

	hasReasoning := false
	hasContent := false
	for _, chunk := range chunks {
		if strings.Contains(chunk, "reasoning_content") && strings.Contains(chunk, "Step by step") {
			hasReasoning = true
		}
		if strings.Contains(chunk, `"content"`) && strings.Contains(chunk, "Final answer.") {
			hasContent = true
		}
	}
	if !hasReasoning {
		t.Error("Expected reasoning_content chunk with 'Step by step'")
	}
	if !hasContent {
		t.Error("Expected content chunk with 'Final answer.'")
	}
}

func TestExtractThinking_AllVariants(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		wantThinking    string
		wantRegular     string
	}{
		{"thinking tag", "<thinking>thought</thinking>answer", "thought", "answer"},
		{"think tag", "<think>thought</think>answer", "thought", "answer"},
		{"reasoning tag", "<reasoning>thought</reasoning>answer", "thought", "answer"},
		{"thought tag", "<thought>thought</thought>answer", "thought", "answer"},
		{"no tag", "just content", "", "just content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thinking, regular := extractThinking(tt.input)
			if thinking != tt.wantThinking {
				t.Errorf("thinking = %q, want %q", thinking, tt.wantThinking)
			}
			if regular != tt.wantRegular {
				t.Errorf("regular = %q, want %q", regular, tt.wantRegular)
			}
		})
	}
}
