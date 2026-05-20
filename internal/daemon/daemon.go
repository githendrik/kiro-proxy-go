package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	PIDFile  = "/tmp/kiro-proxy.pid"
	Shutdown = 10 * time.Second
)

// LogFile is the path to the log file.
var LogFile string

func init() {
	// Set log file to user-writable location
	home, err := os.UserHomeDir()
	if err != nil {
		LogFile = "/tmp/kiro-proxy.log"
		return
	}
	
	// Try ~/.local/state first (XDG spec), fallback to ~
	logDir := filepath.Join(home, ".local", "state")
	if err := os.MkdirAll(logDir, 0755); err == nil {
		LogFile = filepath.Join(logDir, "kiro-proxy.log")
	} else {
		LogFile = filepath.Join(home, "kiro-proxy.log")
	}
}

// Start launches the proxy as a background daemon.
func Start() error {
	// Check if already running
	if pid, err := getPID(); err == nil && pid > 0 {
		return fmt.Errorf("already running (PID %d)", pid)
	}

	// Verify credentials before forking
	if err := checkCredentials(); err != nil {
		return fmt.Errorf("credential check failed: %w", err)
	}

	// Get current executable
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Open log file for appending
	logFile, err := os.OpenFile(LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	// Start the process in background
	cmd := exec.Command(execPath, "run")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	// Detach from parent
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start: %w", err)
	}

	// Wait for the child to validate credentials
	// Use a channel to wait for process exit with timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		// Process exited during startup
		logFile.Close()
		logs, _ := os.ReadFile(LogFile)
		// Show only last few lines to keep error concise
		lines := string(logs)
		return fmt.Errorf("startup failed:\n%s", truncateLogs(lines, 5))
	case <-time.After(3 * time.Second):
		// Process still running after 3 seconds, assume success
	}

	// Write PID file
	if err := os.WriteFile(PIDFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	fmt.Printf("started (PID %d)\n", cmd.Process.Pid)
	fmt.Printf("logs: tail -f %s\n", LogFile)

	return nil
}

// Stop gracefully stops the running daemon.
func Stop() error {
	pid, err := getPID()
	if err != nil {
		return fmt.Errorf("not running (no PID file)")
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(PIDFile)
		return fmt.Errorf("not running (invalid PID)")
	}

	// Check if process is actually running
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		_ = os.Remove(PIDFile)
		return fmt.Errorf("not running (process not found)")
	}

	// Send SIGTERM for graceful shutdown
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = os.Remove(PIDFile)
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	// Wait for process to exit
	done := make(chan error, 1)
	go func() {
		_, err := proc.Wait()
		done <- err
	}()

	select {
	case <-done:
		// Process exited
	case <-time.After(Shutdown):
		// Force kill
		_ = proc.Kill()
	}

	// Cleanup PID file
	_ = os.Remove(PIDFile)

	fmt.Println("stopped")
	return nil
}

// Restart stops and then starts the daemon.
func Restart() error {
	// Stop if running (ignore errors if not running)
	_ = Stop()

	// Small delay to ensure cleanup
	time.Sleep(500 * time.Millisecond)

	// Start fresh
	return Start()
}

// Logs tails the log file.
func Logs() error {
	cmd := exec.Command("tail", "-f", LogFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		_ = cmd.Process.Kill()
		os.Exit(0)
	}()

	return cmd.Run()
}

// getPID reads the PID from the PID file.
func getPID() (int, error) {
	data, err := os.ReadFile(PIDFile)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, err
	}

	return pid, nil
}

// checkCredentials verifies that credentials are valid before starting.
// This runs the proxy briefly in the background to validate auth.
func checkCredentials() error {
	// We'll validate by running a quick check in main
	// For now, just check that the credential file exists if specified
	credsFile := os.Getenv("KIRO_CREDS_FILE")
	if credsFile != "" {
		expanded := expandPath(credsFile)
		if _, err := os.Stat(expanded); err != nil {
			return fmt.Errorf("credentials file not found: %s", expanded)
		}
	}
	
	// Also check config file location
	if credsFile == "" {
		configFile := findConfigFile()
		if configFile != "" {
			// Config file exists, credentials will be validated by main process
			return nil
		}
	}
	
	return nil
}

// findConfigFile searches for a config file in standard locations.
func findConfigFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	locations := []string{
		"kiro-proxy.yaml",
		"kiro-proxy.yml",
	}

	if home != "" {
		locations = append(locations,
			filepath.Join(home, ".config", "kiro-proxy", "config.yaml"),
			filepath.Join(home, ".config", "kiro-proxy", "config.yml"),
		)
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc
		}
	}

	return ""
}

// expandPath expands ~ to home directory.
func expandPath(path string) string {
	if path == "" {
		return path
	}
	if len(path) > 1 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// IsRunning returns true if the daemon is currently running.
func IsRunning() bool {
	pid, err := getPID()
	if err != nil {
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Signal 0 checks if process exists without sending a signal
	return proc.Signal(syscall.Signal(0)) == nil
}

// truncateLogs returns the last n lines of the log output.
func truncateLogs(logs string, n int) string {
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	if len(lines) <= n {
		return logs
	}
	start := len(lines) - n
	return strings.Join(lines[start:], "\n")
}
