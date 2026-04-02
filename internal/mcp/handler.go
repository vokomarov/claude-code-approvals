package mcp

import (
	"log/slog"

	"github.com/vokomarov/claude-code-approvals/internal/approvals"
)

// HandlerOpts configures the permission request handler.
type HandlerOpts struct {
	Store           *approvals.Store
	MacosSeconds    int
	TelegramSeconds int
	TotalSeconds    int
	TimeoutPolicy   string
	OnMacos         func(*approvals.ApprovalRequest)
	OnTelegram      func(*approvals.ApprovalRequest)
}

// HandlePermissionRequest creates a request, runs the state machine, blocks until decided,
// cleans up, and returns the decision value ("allow" or "deny").
func HandlePermissionRequest(opts HandlerOpts, sessionID, toolName, toolInput string) (string, error) {
	req := approvals.NewRequest(sessionID, toolName, toolInput)

	slog.Info("permission request received",
		"id", req.ID,
		"session", sessionID,
		"tool", toolName,
	)

	opts.Store.Add(req)
	defer func() {
		opts.Store.Delete(req.ID)
		req.Cancel()
	}()

	approvals.RunMachine(req, approvals.MachineOpts{
		MacosSeconds:    opts.MacosSeconds,
		TelegramSeconds: opts.TelegramSeconds,
		TotalSeconds:    opts.TotalSeconds,
		TimeoutPolicy:   opts.TimeoutPolicy,
		OnMacos:         opts.OnMacos,
		OnTelegram:      opts.OnTelegram,
	})

	// Block until decided.
	decision := <-req.Decision

	slog.Info("permission request decided",
		"id", req.ID,
		"decision", decision.Value,
		"source", decision.Source,
	)

	return decision.Value, nil
}
