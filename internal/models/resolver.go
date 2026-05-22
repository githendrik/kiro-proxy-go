package models

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"kiro-proxy/internal/client"
)

// Resolver handles model name normalization, aliases, and caching.
type Resolver struct {
	client *client.Client

	mu        sync.RWMutex
	cache     []Model
	cacheTime time.Time
	cacheTTL  time.Duration
}

// Model represents a model available through the Kiro API.
type Model struct {
	ID string `json:"modelId"`
}

// Aliases maps user-friendly names to actual Kiro model IDs.
var Aliases = map[string]string{
	"auto-kiro": "auto",
}

// HiddenFromList are models that work but shouldn't appear in /v1/models.
var HiddenFromList = map[string]bool{
	"auto": true,
}

// FallbackModels are returned when the Kiro API is unreachable.
var FallbackModels = []Model{
	{ID: "auto"},
	{ID: "claude-sonnet-4"},
	{ID: "claude-sonnet-4.5"},
	{ID: "claude-sonnet-4.6"},
	{ID: "claude-haiku-4.5"},
	{ID: "claude-opus-4.5"},
	{ID: "claude-opus-4.6"},
	{ID: "claude-opus-4.7"},
	{ID: "deepseek-3.2"},
	{ID: "glm-5"},
	{ID: "minimax-m2.5"},
	{ID: "qwen3-coder-next"},
}

// NewResolver creates a new model resolver.
func NewResolver(client *client.Client) *Resolver {
	return &Resolver{
		client:   client,
		cacheTTL: 1 * time.Hour,
	}
}

// Resolve normalizes a model name: checks aliases, normalizes format.
func (r *Resolver) Resolve(name string) string {
	// Check aliases first
	if real, ok := Aliases[name]; ok {
		return real
	}

	// Normalize: replace hyphens in version numbers (claude-sonnet-4-5 → claude-sonnet-4.5)
	name = normalizeModelName(name)

	return name
}

// ListModels returns available models (from cache or fallback list).
// Note: The runtime.{region}.kiro.dev endpoint does not support /ListAvailableModels,
// so we always use the fallback list.
func (r *Resolver) ListModels() []Model {
	return r.buildModelList(FallbackModels)
}

// LogAvailableModels prints the list of available models to the log.
func (r *Resolver) LogAvailableModels() {
	models := r.ListModels()
	modelIDs := make([]string, 0, len(models))
	for _, m := range models {
		modelIDs = append(modelIDs, m.ID)
	}
	slog.Info("available models", "models", modelIDs)
}

// buildModelList adds aliases and filters hidden models.
func (r *Resolver) buildModelList(models []Model) []Model {
	var result []Model

	for _, m := range models {
		if !HiddenFromList[m.ID] {
			result = append(result, m)
		}
	}

	// Add aliases as visible models
	for alias := range Aliases {
		result = append(result, Model{ID: alias})
	}

	return result
}

// listModelsResponse is the Kiro API response for listing models.
type listModelsResponse struct {
	Models []Model `json:"models"`
}

func (r *Resolver) fetchModels() ([]Model, error) {
	data, err := r.client.DoJSON(
		"GET",
		"/listAvailableModels",
		nil,
		func() io.Reader { return nil },
	)
	if err != nil {
		return nil, fmt.Errorf("list models request failed: %w", err)
	}

	var resp listModelsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse models response: %w", err)
	}

	if len(resp.Models) == 0 {
		return nil, fmt.Errorf("empty model list from API")
	}

	slog.Info("fetched models from API", "count", len(resp.Models))
	return resp.Models, nil
}

// normalizeModelName handles version number normalization.
// e.g., "claude-sonnet-4-5" → "claude-sonnet-4.5"
func normalizeModelName(name string) string {
	// Pattern: if the name ends with -N-M, convert to -N.M
	parts := strings.Split(name, "-")
	if len(parts) >= 3 {
		last := parts[len(parts)-1]
		secondLast := parts[len(parts)-2]

		// Check if both are numeric
		if isNumeric(last) && isNumeric(secondLast) {
			// Join with dot instead of hyphen
			prefix := strings.Join(parts[:len(parts)-2], "-")
			return prefix + "-" + secondLast + "." + last
		}
	}
	return name
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
