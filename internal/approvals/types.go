package approvals

import (
	"context"
	"time"
)

// Decision is the outcome of an approval request.
type Decision struct {
	Value  string // "allow" | "deny"
	Source string // "macos" | "telegram" | "timeout"
}

// ApprovalRequest holds all state for a single pending permission request.
type ApprovalRequest struct {
	ID          string
	SessionID   string
	ToolName    string
	ToolInput   string // raw JSON from Claude Code
	ProjectPath string // daemon CWD at startup; may be empty
	CreatedAt   time.Time
	// Decision receives the outcome via a non-blocking send. Capacity is 1;
	// the first write wins and subsequent writes are silently dropped.
	// Only notification handlers (OnMacos, OnTelegram) or the total-timeout
	// goroutine write to this channel. The MCP handler reads it and then calls
	// Cancel to stop any remaining machine goroutines.
	Decision chan Decision
	Cancel   context.CancelFunc
}
