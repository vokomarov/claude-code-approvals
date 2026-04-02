package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/config"
	"github.com/vokomarov/claude-code-approvals/internal/daemon"
	"github.com/vokomarov/claude-code-approvals/internal/settings"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "on":
		runOn()
	case "off":
		runOff()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: cc <command>")
	fmt.Fprintln(os.Stderr, "  daemon  Start the approval daemon (used by launchd)")
	fmt.Fprintln(os.Stderr, "  on      Enable notifications (modifies Claude Code settings)")
	fmt.Fprintln(os.Stderr, "  off     Disable notifications (modifies Claude Code settings)")
}

func runDaemon() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "path", cfgPath, "err", err)
		os.Exit(1)
	}

	srv, err := daemon.New(cfg)
	if err != nil {
		slog.Error("failed to create daemon", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := srv.Run(ctx); err != nil {
		slog.Error("daemon error", "err", err)
		os.Exit(1)
	}
}

func runOn() {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	// Check daemon is running
	if !isDaemonHealthy(cfg.Daemon.Port) {
		fmt.Fprintf(os.Stderr, "warning: daemon does not appear to be running on port %d\n", cfg.Daemon.Port)
		fmt.Fprintf(os.Stderr, "         start it with: launchctl start com.vokomarov.cc-approvals\n")
	}

	settingsPath := cfg.Paths.ClaudeSettings
	if err := settings.Enable(settingsPath, cfg.Daemon.Port); err != nil {
		fmt.Fprintf(os.Stderr, "error updating settings: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Notifications enabled.")
	fmt.Printf("   Settings updated: %s\n", settingsPath)
	fmt.Println("   Restart your Claude Code session in PHPStorm to apply.")
}

func runOff() {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	settingsPath := cfg.Paths.ClaudeSettings
	if err := settings.Disable(settingsPath); err != nil {
		fmt.Fprintf(os.Stderr, "error updating settings: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Notifications disabled.")
	fmt.Printf("   Settings updated: %s\n", settingsPath)
	fmt.Println("   Restart your Claude Code session in PHPStorm to apply.")
}

func isDaemonHealthy(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/health", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
