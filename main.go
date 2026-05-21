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

	"kiro-proxy/internal/auth"
	"kiro-proxy/internal/client"
	"kiro-proxy/internal/config"
	"kiro-proxy/internal/daemon"
	"kiro-proxy/internal/handler"
	"kiro-proxy/internal/middleware"
	"kiro-proxy/internal/models"
)

// Version is set at build time via ldflags:
//
//	go build -ldflags "-X main.Version=0.1.3"
var Version = "dev"

func main() {
	// Handle daemon commands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "start":
			if err := daemon.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "stop":
			if err := daemon.Stop(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "restart":
			if err := daemon.Restart(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "logs":
			if err := daemon.Logs(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "run":
			// Run in foreground (called by daemon mode)
			runForeground()
			return
		case "help", "-h", "--help":
			printUsage()
			return
		}
	}

	// Default: run in foreground
	runForeground()
}

func runForeground() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	// CLI flag overrides
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	host := fs.String("host", cfg.Host, "listen host")
	port := fs.Int("port", cfg.Port, "listen port")
	// Skip "run" subcommand if present
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	fs.Parse(args)
	cfg.Host = *host
	cfg.Port = *port

	// Setup structured logging
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevelSlog})
	slog.SetDefault(slog.New(logHandler))

	slog.Info("starting kiro-proxy",
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
	if cfg.CredsFile != "" && !cfg.APIRegionSet {
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
	fmt.Fprintf(w, `{"status":"ok","version":"%s"}`, Version)
}

func printUsage() {
	fmt.Printf(`kiro-proxy - Kiro API Proxy

Usage:
  kiro-proxy [command]

Commands:
  start       Start the proxy as a background daemon
  stop        Stop the running daemon
  restart     Restart the daemon
  logs        View daemon logs (tail -f)
  run         Run in foreground (default)
  help        Show this help message

Examples:
  kiro-proxy start              Start as daemon
  kiro-proxy stop               Stop daemon
  kiro-proxy logs               View logs
  kiro-proxy                    Run in foreground
  kiro-proxy run -port 9000     Run on port 9000

Environment Variables:
  KIRO_CREDS_FILE     Path to kiro-cli credentials file
  REFRESH_TOKEN       Direct refresh token (alternative to file)
  KIRO_REGION         Auth region (default: us-east-1)
  KIRO_API_REGION     API region override
  PROXY_API_KEY       API key for proxy authentication
  SERVER_HOST         Listen host (default: 0.0.0.0)
  SERVER_PORT         Listen port (default: 8000)
  LOG_LEVEL           Log level: debug, info, warn, error (default: info)

Files:
  PID:  %s
  Logs: %s
`, "/tmp/kiro-proxy.pid", daemon.LogFile)
}
