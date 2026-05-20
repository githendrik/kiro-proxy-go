package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"kiro-proxy/internal/auth"
	"kiro-proxy/internal/client"
	"kiro-proxy/internal/config"
	"kiro-proxy/internal/converter"
	"kiro-proxy/internal/models"
	"kiro-proxy/internal/parser"
	"kiro-proxy/internal/streaming"
)

// OpenAIHandler handles OpenAI-compatible API endpoints.
type OpenAIHandler struct {
	client      *client.Client
	authManager *auth.Manager
	resolver    *models.Resolver
	config      *config.Config
}

// NewOpenAIHandler creates a new handler.
func NewOpenAIHandler(client *client.Client, authManager *auth.Manager, resolver *models.Resolver, cfg *config.Config) *OpenAIHandler {
	return &OpenAIHandler{
		client:      client,
		authManager: authManager,
		resolver:    resolver,
		config:      cfg,
	}
}

// ListModels handles GET /v1/models.
func (h *OpenAIHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	modelList := h.resolver.ListModels()

	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	type listResponse struct {
		Object string     `json:"object"`
		Data   []modelObj `json:"data"`
	}

	var data []modelObj
	for _, m := range modelList {
		data = append(data, modelObj{
			ID:      m.ID,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "kiro",
		})
	}

	resp := listResponse{
		Object: "list",
		Data:   data,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ChatCompletions handles POST /v1/chat/completions.
func (h *OpenAIHandler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Parse request
	var req converter.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Failed to parse request: "+err.Error())
		return
	}

	// Resolve model name
	req.Model = h.resolver.Resolve(req.Model)

	slog.Info("chat completion request",
		"model", req.Model,
		"stream", req.Stream,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
	)

	// Convert to Kiro payload
	// ProfileARN is only needed for Desktop auth, not OIDC (Builder ID)
	profileARN := h.authManager.ProfileARN()
	kiroPayload, err := converter.BuildKiroPayload(&req, profileARN, h.config.FakeReasoning, h.config.FakeReasoningMaxTokens)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Failed to convert request: "+err.Error())
		return
	}

	// Marshal payload
	payloadBytes, err := json.MarshalIndent(kiroPayload, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to marshal payload")
		return
	}

	slog.Debug("kiro payload", "size_bytes", len(payloadBytes))
	slog.Debug("kiro payload debug", "json", string(payloadBytes))

	// Make request to Kiro API
	getBody := func() io.Reader {
		return bytes.NewReader(payloadBytes)
	}

	resp, err := h.client.DoStream(http.MethodPost, "/generateAssistantResponse", bytes.NewReader(payloadBytes), getBody)
	if err != nil {
		slog.Error("kiro API request failed", "error", err)
		writeError(w, http.StatusBadGateway, "upstream_error", "Kiro API request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	requestID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())

	if req.Stream {
		h.handleStreaming(w, resp.Body, req.Model, requestID)
	} else {
		h.handleNonStreaming(w, resp.Body, req.Model, requestID)
	}
}

// handleStreaming processes the Kiro response as SSE stream.
func (h *OpenAIHandler) handleStreaming(w http.ResponseWriter, body io.Reader, model, requestID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	p := parser.NewParser()
	sc := streaming.NewStreamConverter(model, requestID, h.config.FakeReasoning)

	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			events, parseErr := p.Feed(buf[:n])
			if parseErr != nil {
				slog.Debug("parse error", "error", parseErr)
			}

			for _, event := range events {
				chunks := sc.ProcessEvent(event)
				for _, chunk := range chunks {
					fmt.Fprint(w, chunk)
					flusher.Flush()
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("stream read error", "error", err)
			break
		}
	}

	// Flush any remaining buffered content from the thinking parser
	remainingChunks := sc.FlushBuffer()
	for _, chunk := range remainingChunks {
		fmt.Fprint(w, chunk)
		flusher.Flush()
	}

	// Send final chunk
	finalChunk := sc.Finish("")
	fmt.Fprint(w, finalChunk)
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleNonStreaming collects the full response and returns it as JSON.
func (h *OpenAIHandler) handleNonStreaming(w http.ResponseWriter, body io.Reader, model, requestID string) {
	resp, err := streaming.CollectResponse(body, model, requestID, h.config.FakeReasoning)
	if err != nil {
		slog.Error("failed to collect response", "error", err)
		writeError(w, http.StatusBadGateway, "upstream_error", "Failed to process response: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// writeError writes an OpenAI-compatible error response.
func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    errType,
			"code":    errType,
		},
	})
}
