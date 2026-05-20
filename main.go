package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kiro-proxy-go/internal/auth"
	"kiro-proxy-go/internal/client"
	"kiro-proxy-go/internal/config"
	"kiro-proxy-go/internal/handler"
	"kiro-proxy-go/internal/middleware"
	"kiro-proxy-go/internal/models"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	// CLI flag overrides
	host := flag.String("host", cfg.Host, "listen host")
	port := flag.Int("port", cfg.Port, "listen port")
	flag.Parse()
	cfg.Host = *host
	cfg.Port = *port

	// Setup structured logging
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})
	slog.SetDefault(slog.New(logHandler))

	slog.Info("starting kiro-proxy-go",
		"host", cfg.Host,
		"port", cfg.Port,
		"region", cfg.Region,
		"api_region", cfg.APIRegion,
		"fake_reasoning", cfg.FakeReasoning,
	)

	// Initialize auth manager
	var authManager *auth.Manager
	if cfg.CredsFile != "" {
		slog.Info("using credentials file", "path", cfg.CredsFile)
		authManager = auth.NewManagerFromFile(cfg.CredsFile, cfg.Region)
	} else {
		slog.Info("using direct refresh token")
		authManager = auth.NewManager(cfg.RefreshToken, cfg.Region)
	}

	// Verify credentials on startup
	if _, err := authManager.GetAccessToken(); err != nil {
		slog.Error("failed to obtain initial access token", "error", err)
		os.Exit(1)
	}
	slog.Info("authentication successful")

	// Update API host from creds file region if no explicit API region override
	if cfg.CredsFile != "" && os.Getenv("KIRO_API_REGION") == "" {
		if fileRegion := authManager.FileRegion(); fileRegion != "" && fileRegion != cfg.APIRegion {
			cfg.APIRegion = fileRegion
			cfg.KiroAPIHost = fmt.Sprintf("https://runtime.%s.kiro.dev", cfg.APIRegion)
			slog.Info("updated API region from credentials file", "api_region", cfg.APIRegion)
		}
	}

	// Initialize HTTP client
	streamTimeout := time.Duration(cfg.StreamingReadTimeout) * time.Second
	kiroClient := client.NewClient(authManager, cfg.KiroAPIHost, cfg.MaxRetries, streamTimeout)

	// Initialize model resolver
	resolver := models.NewResolver(kiroClient)

	// Initialize handlers
	openaiHandler := handler.NewOpenAIHandler(kiroClient, authManager, resolver, cfg)

	// Setup routes
	mux := http.NewServeMux()

	// Health endpoints
	mux.HandleFunc("GET /", healthHandler)
	mux.HandleFunc("GET /health", healthHandler)

	// OpenAI-compatible endpoints
	mux.HandleFunc("GET /v1/models", openaiHandler.ListModels)
	mux.HandleFunc("POST /v1/chat/completions", openaiHandler.ChatCompletions)

	// Apply middleware
	var h http.Handler = mux
	h = middleware.APIKeyAuth(cfg.ProxyAPIKey)(h)
	h = middleware.CORS(h)

	// Create server
	server := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      h,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: time.Duration(cfg.StreamingReadTimeout+30) * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("server listening", "addr", cfg.Addr())
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	slog.Info("server stopped")
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","version":"0.1.0"}`)
}
