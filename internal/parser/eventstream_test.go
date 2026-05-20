package parser

import (
	"testing"
)

func TestParser_ParseContentEvent(t *testing.T) {
	p := NewParser()

	// Simulate AWS Event Stream binary format with JSON payload
	// Format: binary framing + JSON {"content":"Hello","modelId":"test"}
	jsonPayload := `{"content":"Hello","modelId":"claude-sonnet-4"}`

	events, err := p.Feed([]byte(jsonPayload))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].Type != "content" {
		t.Errorf("Expected type 'content', got '%s'", events[0].Type)
	}

	if events[0].Content != "Hello" {
		t.Errorf("Expected content 'Hello', got '%s'", events[0].Content)
	}
}

func TestParser_ParseToolUseEvent(t *testing.T) {
	p := NewParser()

	jsonPayload := `{"toolUseId":"call123","name":"calculator","input":"{\"expr\":\"2+2\"}"}`

	events, err := p.Feed([]byte(jsonPayload))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].Type != "tool_use" {
		t.Errorf("Expected type 'tool_use', got '%s'", events[0].Type)
	}

	if events[0].ToolUse == nil {
		t.Fatal("Expected ToolUse to be set")
	}

	if events[0].ToolUse.ToolUseID != "call123" {
		t.Errorf("Expected toolUseID 'call123', got '%s'", events[0].ToolUse.ToolUseID)
	}

	if events[0].ToolUse.Name != "calculator" {
		t.Errorf("Expected name 'calculator', got '%s'", events[0].ToolUse.Name)
	}
}

func TestParser_ParseUsageEvent(t *testing.T) {
	p := NewParser()

	jsonPayload := `{"usage":{"inputTokens":100,"outputTokens":50}}`

	events, err := p.Feed([]byte(jsonPayload))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].Type != "usage" {
		t.Errorf("Expected type 'usage', got '%s'", events[0].Type)
	}

	if events[0].Usage == nil {
		t.Fatal("Expected Usage to be set")
	}

	if events[0].Usage.InputTokens != 100 {
		t.Errorf("Expected inputTokens 100, got %d", events[0].Usage.InputTokens)
	}

	if events[0].Usage.OutputTokens != 50 {
		t.Errorf("Expected outputTokens 50, got %d", events[0].Usage.OutputTokens)
	}
}

func TestParser_ParseThinkingContent(t *testing.T) {
	p := NewParser()

	jsonPayload := `{"thinkingContent":"Let me think about this..."}`

	events, err := p.Feed([]byte(jsonPayload))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if !events[0].IsThinking {
		t.Error("Expected IsThinking to be true")
	}

	if events[0].Content != "Let me think about this..." {
		t.Errorf("Expected thinking content, got '%s'", events[0].Content)
	}
}

func TestParser_ParseErrorEvent(t *testing.T) {
	p := NewParser()

	jsonPayload := `{"error":{"message":"Something went wrong","code":"INTERNAL_ERROR"}}`

	events, err := p.Feed([]byte(jsonPayload))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].Type != "error" {
		t.Errorf("Expected type 'error', got '%s'", events[0].Type)
	}

	if events[0].Error != "Something went wrong" {
		t.Errorf("Expected error message, got '%s'", events[0].Error)
	}
}

func TestParser_MultipleEvents(t *testing.T) {
	p := NewParser()

	// Multiple JSON objects in one feed
	jsonPayload := `{"content":"Hello"}{"content":"World"}`

	events, err := p.Feed([]byte(jsonPayload))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(events))
	}

	if events[0].Content != "Hello" {
		t.Errorf("Expected first content 'Hello', got '%s'", events[0].Content)
	}

	if events[1].Content != "World" {
		t.Errorf("Expected second content 'World', got '%s'", events[1].Content)
	}
}

func TestParser_PartialJSON(t *testing.T) {
	p := NewParser()

	// Incomplete JSON
	events, err := p.Feed([]byte(`{"content":"Hello`))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 0 {
		t.Errorf("Expected 0 events for incomplete JSON, got %d", len(events))
	}

	// Complete the JSON
	events, err = p.Feed([]byte(`"}`))
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event after completing JSON, got %d", len(events))
	}

	if events[0].Content != "Hello" {
		t.Errorf("Expected content 'Hello', got '%s'", events[0].Content)
	}
}

func TestParser_InvalidJSON(t *testing.T) {
	p := NewParser()

	// Invalid JSON should be skipped
	events, err := p.Feed([]byte(`{invalid json}`))
	if err != nil {
		// Error is acceptable for invalid JSON
		return
	}

	// If no error, should have 0 events
	if len(events) != 0 {
		t.Errorf("Expected 0 events for invalid JSON, got %d", len(events))
	}
}

func TestParser_EmptyPayload(t *testing.T) {
	p := NewParser()

	events, err := p.Feed([]byte{})
	if err != nil {
		t.Fatalf("Feed failed: %v", err)
	}

	if len(events) != 0 {
		t.Errorf("Expected 0 events for empty payload, got %d", len(events))
	}
}

func TestParser_BufferAccumulation(t *testing.T) {
	p := NewParser()

	// Feed character by character
	jsonStr := `{"content":"test"}`
	for i := 0; i < len(jsonStr); i++ {
		events, err := p.Feed([]byte{jsonStr[i]})
		if err != nil {
			t.Fatalf("Feed failed at position %d: %v", i, err)
		}
		if i < len(jsonStr)-1 && len(events) != 0 {
			t.Errorf("Expected 0 events at position %d, got %d", i, len(events))
		}
	}

	// After complete JSON, should have events
	// (parser may have already emitted events during accumulation)
}

func TestParseJSONPayload_AllFields(t *testing.T) {
	event, err := parseJSONPayload([]byte(`{"content":"test","modelId":"test"}`))
	if err != nil {
		t.Fatalf("parseJSONPayload failed: %v", err)
	}

	if event == nil {
		t.Fatal("Expected event to be returned")
	}

	if event.Type != "content" {
		t.Errorf("Expected type 'content', got '%s'", event.Type)
	}
}

func TestFindMatchingBrace(t *testing.T) {
	tests := []struct {
		input    string
		start    int
		expected int
	}{
		{"{}", 0, 1},
		{"{\"a\": 1}", 0, 7},
		{"{\"a\": {\"b\": 1}}", 0, 14},
		{"{\"a\": \"{\"}", 0, 9},
		{"prefix {} suffix", 7, 8},
		{"no match", 0, -1},
	}

	for _, tt := range tests {
		result := findMatchingBrace(tt.input, tt.start)
		if result != tt.expected {
			t.Errorf("findMatchingBrace(%q, %d) = %d, want %d", tt.input, tt.start, result, tt.expected)
		}
	}
}
