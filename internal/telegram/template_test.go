package telegram_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/telegram"
)

func TestDefaultTemplate(t *testing.T) {
	data := telegram.TemplateData{
		SessionID: "sess-123",
		ToolName:  "Bash",
		ToolInput: `{"command":"ls"}`,
		CreatedAt: time.Date(2026, 3, 31, 14, 30, 0, 0, time.UTC).Format("15:04:05"),
	}
	msg, err := telegram.RenderMessage("", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "sess-123") {
		t.Error("expected SessionID in message")
	}
	if !strings.Contains(msg, "Bash") {
		t.Error("expected ToolName in message")
	}
	if !strings.Contains(msg, `{"command":"ls"}`) {
		t.Error("expected ToolInput in message")
	}
}

func TestCustomTemplate(t *testing.T) {
	tmpl := "tool={{.ToolName}} session={{.SessionID}}"
	data := telegram.TemplateData{
		SessionID: "abc",
		ToolName:  "Write",
		ToolInput: "{}",
		CreatedAt: time.Now().Format("15:04:05"),
	}
	msg, err := telegram.RenderMessage(tmpl, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "tool=Write session=abc" {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestInvalidTemplate(t *testing.T) {
	_, err := telegram.RenderMessage("{{.Undefined", telegram.TemplateData{})
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestToolInputTruncation(t *testing.T) {
	big := strings.Repeat("x", 4000)
	data := telegram.TemplateData{
		SessionID: "s",
		ToolName:  "Bash",
		ToolInput: big,
		CreatedAt: time.Now().Format("15:04:05"),
	}
	msg, err := telegram.RenderMessage("", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg) > 4096 {
		t.Errorf("message exceeds Telegram limit: %d chars", len(msg))
	}
	if !strings.Contains(msg, "...[truncated]") {
		t.Error("expected truncation marker")
	}
}
