package middleware

import (
	"net/http"
	"strings"
)

// APIKeyAuth creates middleware that validates the proxy API key.
func APIKeyAuth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for health endpoints
			if r.URL.Path == "/" || r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			// Check Authorization: Bearer <key>
			auth := r.Header.Get("Authorization")
			if auth != "" {
				token := strings.TrimPrefix(auth, "Bearer ")
				if token == apiKey {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Check x-api-key header (Anthropic style)
			if r.Header.Get("x-api-key") == apiKey {
				next.ServeHTTP(w, r)
				return
			}

			http.Error(w, `{"error":{"message":"Invalid API key","type":"authentication_error","code":"invalid_api_key"}}`, http.StatusUnauthorized)
		})
	}
}

// CORS adds permissive CORS headers for tool compatibility.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
