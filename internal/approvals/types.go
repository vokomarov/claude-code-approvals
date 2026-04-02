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
	Decision    chan Decision // capacity 1; first write wins
	Cancel      context.CancelFunc
}
