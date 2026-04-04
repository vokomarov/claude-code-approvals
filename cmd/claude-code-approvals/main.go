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
	"github.com/vokomarov/claude-code-approvals/internal/hook"
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
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "on":
		runOn()
	case "off":
		runOff()
	case "hook":
		hook.Run()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: claude-code-approvals <command>")
	fmt.Fprintln(os.Stderr, "  daemon     Start the approval daemon (used by launchd)")
	fmt.Fprintln(os.Stderr, "  install    One-time: register hook in Claude Code settings.json")
	fmt.Fprintln(os.Stderr, "  uninstall  Remove hook from Claude Code settings.json")
	fmt.Fprintln(os.Stderr, "  on         Enable approval intercepting (daemon must be running)")
	fmt.Fprintln(os.Stderr, "  off        Disable approval intercepting (daemon stays running)")
	fmt.Fprintln(os.Stderr, "  hook       Run as Claude Code PermissionRequest hook (invoked by Claude Code)")
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

func runInstall() {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	binaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving binary path: %v\n", err)
		os.Exit(1)
	}

	settingsPath := cfg.Paths.ClaudeSettings
	if err := settings.Install(settingsPath, binaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "error updating settings: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Hook installed.")
	fmt.Printf("   Settings updated: %s\n", settingsPath)
	fmt.Println("   This is a one-time setup — run 'on'/'off' to toggle without restarting Claude Code.")
}

func runUninstall() {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	settingsPath := cfg.Paths.ClaudeSettings
	if err := settings.Uninstall(settingsPath); err != nil {
		fmt.Fprintf(os.Stderr, "error updating settings: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Hook uninstalled.")
	fmt.Printf("   Settings updated: %s\n", settingsPath)
}

func runOn() {
	port := daemonPort()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(fmt.Sprintf("http://localhost:%d/api/enable", port), "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: daemon not reachable at localhost:%d\n", port)
		fmt.Fprintf(os.Stderr, "       start it with: launchctl start com.vokomarov.cc-approvals\n")
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Println("Approval intercepting enabled.")
}

func runOff() {
	port := daemonPort()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(fmt.Sprintf("http://localhost:%d/api/disable", port), "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: daemon not reachable at localhost:%d\n", port)
		fmt.Fprintf(os.Stderr, "       start it with: launchctl start com.vokomarov.cc-approvals\n")
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Println("Approval intercepting disabled.")
}

// daemonPort returns the daemon port from config, or the default 9753 on error.
func daemonPort() int {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return 9753
	}
	return cfg.Daemon.Port
}
