package streaming

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"kiro-proxy/internal/parser"
)

// OpenAI SSE response types

// ChatCompletionChunk is the OpenAI streaming response format.
type ChatCompletionChunk struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Choices           []ChunkChoice  `json:"choices"`
	Usage             *ChunkUsage    `json:"usage,omitempty"`
}

// ChunkChoice is a single choice in a streaming chunk.
type ChunkChoice struct {
	Index        int          `json:"index"`
	Delta        ChunkDelta   `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

// ChunkDelta is the delta content in a streaming chunk.
type ChunkDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          *string         `json:"content,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ChunkToolCall `json:"tool_calls,omitempty"`
}

// ChunkToolCall is a tool call delta in streaming.
type ChunkToolCall struct {
	Index    int                   `json:"index"`
	ID       string                `json:"id,omitempty"`
	Type     string                `json:"type,omitempty"`
	Function ChunkToolCallFunction `json:"function"`
}

// ChunkToolCallFunction is the function delta in a streaming tool call.
type ChunkToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ChunkUsage is token usage in the final chunk.
type ChunkUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionResponse is the non-streaming OpenAI response format.
type ChatCompletionResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []ResponseChoice   `json:"choices"`
	Usage   ChunkUsage         `json:"usage"`
}

// ResponseChoice is a choice in a non-streaming response.
type ResponseChoice struct {
	Index        int             `json:"index"`
	Message      ResponseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

// ResponseMessage is the message in a non-streaming response.
type ResponseMessage struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall is a tool call in a non-streaming response.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc is the function in a non-streaming tool call.
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ThinkingState tracks whether we're inside a thinking block.
type ThinkingState int

const (
	ThinkingNone    ThinkingState = iota
	ThinkingActive               // inside <thinking> block
	ThinkingDone                 // after </thinking>, now in content
)

// thinkingTagPair represents an opening and closing tag for thinking blocks.
type thinkingTagPair struct {
	Open  string
	Close string
}

// supportedThinkingTags lists all recognized thinking tag variants.
// Matches the Python reference: <thinking>, <think>, <reasoning>, <thought>
var supportedThinkingTags = []thinkingTagPair{
	{"<thinking>", "</thinking>"},
	{"<think>", "</think>"},
	{"<reasoning>", "</reasoning>"},
	{"<thought>", "</thought>"},
}

// StreamConverter converts Kiro event stream to OpenAI SSE chunks.
type StreamConverter struct {
	model         string
	requestID     string
	created       int64
	fakeReasoning bool

	thinkingState  ThinkingState
	matchedTag     *thinkingTagPair // which tag pair was matched for the current thinking block
	contentBuffer  strings.Builder
	toolCalls      map[string]*accumulatedToolCall
	toolCallOrder  []string
	sentRole       bool
	sentContent    bool // tracks whether regular content has been emitted
}

type accumulatedToolCall struct {
	ID    string
	Name  string
	Input strings.Builder
	Index int
}

// NewStreamConverter creates a new stream converter.
func NewStreamConverter(model, requestID string, fakeReasoning bool) *StreamConverter {
	return &StreamConverter{
		model:         model,
		requestID:     requestID,
		created:       time.Now().Unix(),
		fakeReasoning: fakeReasoning,
		toolCalls:     make(map[string]*accumulatedToolCall),
	}
}

// ProcessEvent converts a Kiro event to zero or more SSE chunks.
func (sc *StreamConverter) ProcessEvent(event parser.Event) []string {
	switch event.Type {
	case "content":
		// Native thinking events from Kiro API (thinkingContent field)
		// should be emitted directly as reasoning_content, bypassing the tag parser.
		// However, if regular content has already been emitted, suppress late-arriving
		// thinking events to prevent them from appearing after the answer.
		if event.IsThinking {
			if sc.sentContent {
				// Late thinking event after content - discard to prevent misordering
				return nil
			}
			return sc.emitReasoning(event.Content)
		}
		return sc.processContent(event.Content)
	case "tool_use":
		return sc.processToolUse(event.ToolUse)
	case "error":
		return sc.processError(event.Error)
	default:
		return nil
	}
}

// Finish returns the final chunk with finish_reason and usage.
func (sc *StreamConverter) Finish(finishReason string) string {
	if finishReason == "" {
		if len(sc.toolCalls) > 0 {
			finishReason = "tool_calls"
		} else {
			finishReason = "stop"
		}
	}

	chunk := ChatCompletionChunk{
		ID:      sc.requestID,
		Object:  "chat.completion.chunk",
		Created: sc.created,
		Model:   sc.model,
		Choices: []ChunkChoice{
			{
				Index:        0,
				Delta:        ChunkDelta{},
				FinishReason: &finishReason,
			},
		},
	}

	return formatSSE(chunk)
}

// processContent handles content events, parsing thinking tags if needed.
func (sc *StreamConverter) processContent(content string) []string {
	if !sc.fakeReasoning {
		return sc.emitContent(content)
	}

	// Handle thinking tag parsing
	return sc.processWithThinking(content)
}

// processWithThinking handles the thinking tag state machine.
func (sc *StreamConverter) processWithThinking(content string) []string {
	var chunks []string

	// Accumulate content and process thinking tags
	sc.contentBuffer.WriteString(content)
	buffered := sc.contentBuffer.String()

	for {
		switch sc.thinkingState {
		case ThinkingNone:
			// Look for any supported opening tag
			bestIdx := -1
			var bestTag *thinkingTagPair
			for i := range supportedThinkingTags {
				idx := strings.Index(buffered, supportedThinkingTags[i].Open)
				if idx != -1 && (bestIdx == -1 || idx < bestIdx) {
					bestIdx = idx
					bestTag = &supportedThinkingTags[i]
				}
			}

			if bestIdx == -1 {
				// Check if we might be in the middle of a tag
				if hasPartialThinkingTag(buffered) {
					// Hold buffer, wait for more data
					sc.contentBuffer.Reset()
					sc.contentBuffer.WriteString(buffered)
					return chunks
				}
				// No thinking tag, emit as regular content
				if buffered != "" {
					chunks = append(chunks, sc.emitContent(buffered)...)
					buffered = ""
				}
				sc.contentBuffer.Reset()
				return chunks
			}

			// Emit any content before the tag
			if bestIdx > 0 {
				chunks = append(chunks, sc.emitContent(buffered[:bestIdx])...)
			}
			buffered = buffered[bestIdx+len(bestTag.Open):]
			sc.matchedTag = bestTag
			sc.thinkingState = ThinkingActive

		case ThinkingActive:
			// Look for the matching closing tag
			closeTag := sc.matchedTag.Close
			idx := strings.Index(buffered, closeTag)
			if idx == -1 {
				// Check for partial closing tag
				if containsPartialTag(buffered, closeTag) {
					sc.contentBuffer.Reset()
					sc.contentBuffer.WriteString(buffered)
					return chunks
				}
				// All buffered content is thinking
				if buffered != "" {
					chunks = append(chunks, sc.emitReasoning(buffered)...)
					buffered = ""
				}
				sc.contentBuffer.Reset()
				return chunks
			}

			// Emit thinking content before the closing tag
			if idx > 0 {
				chunks = append(chunks, sc.emitReasoning(buffered[:idx])...)
			}
			buffered = buffered[idx+len(closeTag):]
			sc.thinkingState = ThinkingDone

		case ThinkingDone:
			// Everything after closing tag is regular content
			if buffered != "" {
				// Strip leading newlines after thinking block
				buffered = strings.TrimLeft(buffered, "\n")
				if buffered != "" {
					chunks = append(chunks, sc.emitContent(buffered)...)
				}
				buffered = ""
			}
			sc.contentBuffer.Reset()
			return chunks
		}
	}
}

// hasPartialThinkingTag checks if the buffer ends with a partial match
// of any supported thinking opening tag.
func hasPartialThinkingTag(s string) bool {
	for _, tag := range supportedThinkingTags {
		if containsPartialTag(s, tag.Open) {
			return true
		}
	}
	return strings.HasSuffix(s, "<")
}

// containsPartialTag checks if the end of s could be the start of tag.
func containsPartialTag(s, tag string) bool {
	for i := 1; i < len(tag) && i <= len(s); i++ {
		if strings.HasSuffix(s, tag[:i]) {
			return true
		}
	}
	return false
}

// emitContent creates an SSE chunk with regular content.
func (sc *StreamConverter) emitContent(content string) []string {
	sc.sentContent = true

	chunk := ChatCompletionChunk{
		ID:      sc.requestID,
		Object:  "chat.completion.chunk",
		Created: sc.created,
		Model:   sc.model,
		Choices: []ChunkChoice{
			{
				Index: 0,
				Delta: ChunkDelta{Content: &content},
			},
		},
	}

	if !sc.sentRole {
		chunk.Choices[0].Delta.Role = "assistant"
		sc.sentRole = true
	}

	return []string{formatSSE(chunk)}
}

// emitReasoning creates an SSE chunk with reasoning content.
func (sc *StreamConverter) emitReasoning(content string) []string {
	chunk := ChatCompletionChunk{
		ID:      sc.requestID,
		Object:  "chat.completion.chunk",
		Created: sc.created,
		Model:   sc.model,
		Choices: []ChunkChoice{
			{
				Index: 0,
				Delta: ChunkDelta{ReasoningContent: &content},
			},
		},
	}

	if !sc.sentRole {
		chunk.Choices[0].Delta.Role = "assistant"
		sc.sentRole = true
	}

	return []string{formatSSE(chunk)}
}

// processToolUse handles tool use events.
func (sc *StreamConverter) processToolUse(tu *parser.ToolUseEvent) []string {
	if tu == nil {
		return nil
	}

	tc, exists := sc.toolCalls[tu.ToolUseID]
	if !exists {
		idx := len(sc.toolCallOrder)
		tc = &accumulatedToolCall{
			ID:    tu.ToolUseID,
			Name:  tu.Name,
			Index: idx,
		}
		sc.toolCalls[tu.ToolUseID] = tc
		sc.toolCallOrder = append(sc.toolCallOrder, tu.ToolUseID)

		// Emit initial tool call chunk with name
		chunk := ChatCompletionChunk{
			ID:      sc.requestID,
			Object:  "chat.completion.chunk",
			Created: sc.created,
			Model:   sc.model,
			Choices: []ChunkChoice{
				{
					Index: 0,
					Delta: ChunkDelta{
						ToolCalls: []ChunkToolCall{
							{
								Index: idx,
								ID:    tu.ToolUseID,
								Type:  "function",
								Function: ChunkToolCallFunction{
									Name:      tu.Name,
									Arguments: tu.Input,
								},
							},
						},
					},
				},
			},
		}

		if !sc.sentRole {
			chunk.Choices[0].Delta.Role = "assistant"
			sc.sentRole = true
		}

		tc.Input.WriteString(tu.Input)
		return []string{formatSSE(chunk)}
	}

	// Subsequent input delta
	tc.Input.WriteString(tu.Input)

	chunk := ChatCompletionChunk{
		ID:      sc.requestID,
		Object:  "chat.completion.chunk",
		Created: sc.created,
		Model:   sc.model,
		Choices: []ChunkChoice{
			{
				Index: 0,
				Delta: ChunkDelta{
					ToolCalls: []ChunkToolCall{
						{
							Index: tc.Index,
							Function: ChunkToolCallFunction{
								Arguments: tu.Input,
							},
						},
					},
				},
			},
		},
	}

	return []string{formatSSE(chunk)}
}

// FlushBuffer flushes any remaining content in the thinking parser buffer.
// Call this before Finish() to ensure no content is lost.
func (sc *StreamConverter) FlushBuffer() []string {
	if sc.contentBuffer.Len() == 0 {
		return nil
	}

	buffered := sc.contentBuffer.String()
	sc.contentBuffer.Reset()

	if sc.thinkingState == ThinkingActive {
		// Unclosed thinking block - if regular content was already sent,
		// suppress to prevent misordered thinking at end of stream.
		if sc.sentContent {
			return nil
		}
		return sc.emitReasoning(buffered)
	}
	// Regular content
	if buffered != "" {
		return sc.emitContent(buffered)
	}
	return nil
}

// processError handles error events.
func (sc *StreamConverter) processError(errMsg string) []string {
	content := fmt.Sprintf("[Error: %s]", errMsg)
	return sc.emitContent(content)
}

// CollectResponse collects all events into a non-streaming response.
func CollectResponse(body io.Reader, model, requestID string, fakeReasoning bool) (*ChatCompletionResponse, error) {
	p := parser.NewParser()
	sc := NewStreamConverter(model, requestID, fakeReasoning)

	var allContent strings.Builder
	var allReasoning strings.Builder
	var toolCalls []ToolCall

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			events, parseErr := p.Feed(buf[:n])
			if parseErr != nil {
				return nil, parseErr
			}

			for _, event := range events {
				chunks := sc.ProcessEvent(event)
				for _, chunk := range chunks {
					// Parse the SSE chunk to extract content
					_ = chunk // We accumulate from events directly
				}

				switch event.Type {
				case "content":
					// For non-streaming, we need to handle thinking differently
					if fakeReasoning {
						// Simple approach: accumulate and parse at the end
						allContent.WriteString(event.Content)
					} else {
						allContent.WriteString(event.Content)
					}
				case "tool_use":
					if event.ToolUse != nil {
						found := false
						for i, tc := range toolCalls {
							if tc.ID == event.ToolUse.ToolUseID {
								toolCalls[i].Function.Arguments += event.ToolUse.Input
								found = true
								break
							}
						}
						if !found {
							toolCalls = append(toolCalls, ToolCall{
								ID:   event.ToolUse.ToolUseID,
								Type: "function",
								Function: ToolCallFunc{
									Name:      event.ToolUse.Name,
									Arguments: event.ToolUse.Input,
								},
							})
						}
					}
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read error: %w", err)
		}
	}

	// Parse thinking from accumulated content if fake reasoning
	content := allContent.String()
	var reasoning *string
	if fakeReasoning {
		r, c := extractThinking(content)
		if r != "" {
			reasoning = &r
			allReasoning.WriteString(r)
		}
		content = c
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	var contentPtr *string
	if content != "" {
		contentPtr = &content
	}

	resp := &ChatCompletionResponse{
		ID:      requestID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ResponseChoice{
			{
				Index: 0,
				Message: ResponseMessage{
					Role:             "assistant",
					Content:          contentPtr,
					ReasoningContent: reasoning,
					ToolCalls:        toolCalls,
				},
				FinishReason: finishReason,
			},
		},
		Usage: ChunkUsage{}, // We don't have accurate token counts without tiktoken
	}

	return resp, nil
}

// extractThinking separates thinking content from regular content.
// Supports all recognized thinking tag variants.
func extractThinking(content string) (thinking, regular string) {
	for _, tag := range supportedThinkingTags {
		startIdx := strings.Index(content, tag.Open)
		if startIdx == -1 {
			continue
		}

		endIdx := strings.Index(content, tag.Close)
		if endIdx == -1 {
			// Unclosed thinking tag - treat everything after as thinking
			return content[startIdx+len(tag.Open):], content[:startIdx]
		}

		thinking = content[startIdx+len(tag.Open) : endIdx]
		regular = content[:startIdx] + strings.TrimLeft(content[endIdx+len(tag.Close):], "\n")
		return thinking, regular
	}

	return "", content
}

// formatSSE formats a chunk as an SSE data line.
func formatSSE(chunk ChatCompletionChunk) string {
	data, _ := json.Marshal(chunk)
	return fmt.Sprintf("data: %s\n\n", string(data))
}
