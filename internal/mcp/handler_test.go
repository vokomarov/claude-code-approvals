package mcp_test

import (
	"testing"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/approvals"
	"github.com/vokomarov/claude-code-approvals/internal/mcp"
)

func TestHandlePermissionRequestAutoApprove(t *testing.T) {
	store := approvals.NewStore()
	opts := mcp.HandlerOpts{
		Store:           store,
		MacosSeconds:    0,
		TelegramSeconds: 0,
		TotalSeconds:    1,
		TimeoutPolicy:   "allow",
		OnMacos:         func(r *approvals.ApprovalRequest) {},
		OnTelegram:      func(r *approvals.ApprovalRequest) {},
	}

	start := time.Now()
	decision, err := mcp.HandlePermissionRequest(opts, "sess1", "Bash", `{"command":"ls"}`)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != "allow" {
		t.Errorf("expected allow from timeout policy, got %q", decision)
	}
	if elapsed < time.Second || elapsed > 3*time.Second {
		t.Errorf("expected ~1s elapsed, got %v", elapsed)
	}
}

func TestHandlePermissionRequestCleanup(t *testing.T) {
	store := approvals.NewStore()
	opts := mcp.HandlerOpts{
		Store:           store,
		MacosSeconds:    0,
		TelegramSeconds: 0,
		TotalSeconds:    1,
		TimeoutPolicy:   "deny",
		OnMacos:         func(r *approvals.ApprovalRequest) {},
		OnTelegram:      func(r *approvals.ApprovalRequest) {},
	}

	_, _ = mcp.HandlePermissionRequest(opts, "s", "Bash", "{}")

	// After completion, store should be empty
	all := store.All()
	if len(all) != 0 {
		t.Errorf("expected store to be empty after completion, got %d requests", len(all))
	}
}
