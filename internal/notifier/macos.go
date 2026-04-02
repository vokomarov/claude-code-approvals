package notifier

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

const maxMacOSMessageLen = 200

// TruncateForMacOS truncates a string to 200 characters for the macOS notification message.
func TruncateForMacOS(s string) string {
	if len(s) <= maxMacOSMessageLen {
		return s
	}
	return s[:197] + "..."
}

// IsAvailable returns true if terminal-notifier is on PATH.
func IsAvailable() bool {
	_, err := exec.LookPath("terminal-notifier")
	return err == nil
}

// Notify sends a macOS notification with Approve/Deny buttons and returns the
// button pressed ("Approve" or "Deny"), or an empty string if dismissed without interaction.
// It blocks until the notification is interacted with or the context is cancelled.
//
// The -activate flag focuses phpstormBundleID only when the notification body is clicked.
// Approve/Deny button presses do NOT focus any app.
func Notify(ctx context.Context, title, message, phpstormBundleID string, timeoutSeconds int) (string, error) {
	args := []string{
		"-title", title,
		"-message", message,
		"-actions", "Approve,Deny",
		"-activate", phpstormBundleID,
		"-timeout", fmt.Sprintf("%d", timeoutSeconds),
	}
	cmd := exec.CommandContext(ctx, "terminal-notifier", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start terminal-notifier: %w", err)
	}

	var result string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "Approve" || line == "Deny" {
			result = line
		}
	}

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		slog.Warn("terminal-notifier exited with error", "err", err)
	}
	return result, nil
}
