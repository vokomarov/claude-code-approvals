// Package hook implements the hook subcommand that bridges Claude Code's
// PermissionRequest hook mechanism to the cc-approvals daemon.
package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/config"
)

// hookInput is the JSON received from Claude Code on stdin for a PermissionRequest hook.
type hookInput struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	CWD       string          `json:"cwd"`
}

// daemonResponse is the JSON returned by POST /api/permission.
type daemonResponse struct {
	Decision string `json:"decision"`
}

// hookDecision represents the decision object in the hook output.
type hookDecision struct {
	Behavior string `json:"behavior"`
}

// hookSpecificOutput represents the hook-specific output structure.
type hookSpecificOutput struct {
	HookEventName string       `json:"hookEventName"`
	Decision      hookDecision `json:"decision"`
}

// hookOutput represents the complete output structure sent back to Claude Code.
type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

// Run is the hook subcommand entrypoint. Reads from os.Stdin, writes to os.Stdout.
// Always exits cleanly; errors result in no output, causing Claude Code to fall back
// to its built-in interactive permission prompt.
func Run() {
	port := 9753
	timeout := 310 * time.Second
	if cfg, err := config.Load(config.DefaultPath()); err == nil {
		port = cfg.Daemon.Port
		timeout = time.Duration(cfg.Timeouts.TotalTimeoutSeconds+10) * time.Second
	}
	run(os.Stdin, os.Stdout, fmt.Sprintf("http://localhost:%d", port), timeout)
}

// run is the testable core of the hook subcommand.
func run(in io.Reader, out io.Writer, daemonBaseURL string, clientTimeout time.Duration) {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return
	}

	body, err := json.Marshal(input)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: clientTimeout}
	resp, err := client.Post(daemonBaseURL+"/api/permission", "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var dr daemonResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return
	}

	output := hookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName: "PermissionRequest",
			Decision:      hookDecision{Behavior: dr.Decision},
		},
	}
	_ = json.NewEncoder(out).Encode(output)
}
