package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"kiro-proxy/internal/models"
)

// DebugHandler handles debug/internal endpoints.
type DebugHandler struct {
	resolver *models.Resolver
}

// NewDebugHandler creates a new debug handler.
func NewDebugHandler(resolver *models.Resolver) *DebugHandler {
	return &DebugHandler{
		resolver: resolver,
	}
}

// Models handles GET /debug/models.
func (h *DebugHandler) Models(w http.ResponseWriter, r *http.Request) {
	modelList := h.resolver.ListModels()

	type modelInfo struct {
		ID string `json:"id"`
	}

	type listResponse struct {
		Object string      `json:"object"`
		Data   []modelInfo `json:"data"`
		Count  int         `json:"count"`
	}

	var data []modelInfo
	for _, m := range modelList {
		data = append(data, modelInfo{ID: m.ID})
	}

	resp := listResponse{
		Object: "list",
		Data:   data,
		Count:  len(data),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode models response", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
