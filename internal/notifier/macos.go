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

// TruncateForMacOS truncates a string to 200 Unicode characters for the macOS notification message.
func TruncateForMacOS(s string) string {
	runes := []rune(s)
	if len(runes) <= maxMacOSMessageLen {
		return s
	}
	return string(runes[:197]) + "..."
}

// IsAvailable returns true if alerter is on PATH.
func IsAvailable() bool {
	_, err := exec.LookPath("alerter")
	return err == nil
}

// Notify sends a macOS notification with Approve/Deny buttons and returns the
// button pressed ("Approve" or "Deny"), or an empty string if dismissed without interaction.
// It blocks until the notification is interacted with or the context is cancelled.
// groupID tags the notification so it can be removed from the Notification Center if
// the context is cancelled before the user interacts (e.g. decision arrived via Telegram).
func Notify(ctx context.Context, groupID, title, message string, timeoutSeconds int) (string, error) {
	args := []string{
		"--group", groupID,
		"--title", title,
		"--message", message,
		"--actions", "Approve,Deny",
		"--timeout", fmt.Sprintf("%d", timeoutSeconds),
	}
	cmd := exec.CommandContext(ctx, "alerter", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start alerter: %w", err)
	}

	var result string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "Approve" || line == "Deny" {
			result = line
			break // first valid action wins; stop reading
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("error reading alerter output", "err", err)
	}

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		slog.Warn("alerter exited with error", "err", err)
	}

	// If the context was cancelled (decision arrived via another channel), remove
	// the notification from the Notification Center so the user does not see a
	// stale prompt after they have already decided.
	if ctx.Err() != nil {
		if removeErr := exec.Command("alerter", "--remove", groupID).Run(); removeErr != nil {
			slog.Warn("failed to remove macOS notification", "group", groupID, "err", removeErr)
		}
	}

	// result is "Approve", "Deny", or "" (dismissed / timed out / context cancelled)
	return result, nil
}
