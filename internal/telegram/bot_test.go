package telegram_test

import (
	"testing"

	"github.com/vokomarov/claude-code-approvals/internal/telegram"
)

func TestCallbackDataFormat(t *testing.T) {
	id := "550e8400-e29b-41d4-a716-446655440000"
	approve := telegram.CallbackData("approve", id)
	deny := telegram.CallbackData("deny", id)

	if approve != "approve:550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("unexpected approve callback: %q", approve)
	}
	if deny != "deny:550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("unexpected deny callback: %q", deny)
	}
	if len(approve) > 64 {
		t.Errorf("callback data exceeds Telegram 64-byte limit: %d", len(approve))
	}
}

func TestParseCallback(t *testing.T) {
	decision, id, err := telegram.ParseCallback("approve:abc-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != "allow" {
		t.Errorf("expected allow, got %q", decision)
	}
	if id != "abc-123" {
		t.Errorf("expected id=abc-123, got %q", id)
	}

	decision2, id2, err2 := telegram.ParseCallback("deny:xyz-456")
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}
	if decision2 != "deny" {
		t.Errorf("expected deny, got %q", decision2)
	}
	if id2 != "xyz-456" {
		t.Errorf("expected id=xyz-456, got %q", id2)
	}
}

func TestParseCallbackInvalid(t *testing.T) {
	_, _, err := telegram.ParseCallback("invalid")
	if err == nil {
		t.Error("expected error for invalid callback data")
	}
	_, _, err2 := telegram.ParseCallback("unknown:id")
	if err2 == nil {
		t.Error("expected error for unknown action")
	}
}
