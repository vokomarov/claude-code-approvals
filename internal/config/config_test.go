package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vokomarov/claude-code-approvals/internal/config"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 123
timeouts:
  macos_notification_seconds: 15
  telegram_notification_seconds: 30
  total_timeout_seconds: 300
  timeout_policy: deny
macos:
  phpstorm_bundle_id: "com.jetbrains.phpstorm"
daemon:
  port: 9753
paths:
  claude_settings: "~/.claude/settings.json"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.BotToken != "tok" {
		t.Errorf("expected bot_token=tok, got %q", cfg.Telegram.BotToken)
	}
	if cfg.Timeouts.MacosNotificationSeconds != 15 {
		t.Errorf("expected macos=15, got %d", cfg.Timeouts.MacosNotificationSeconds)
	}
}

func TestValidationBothTimeoutsZeroIsAllowed(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 123
timeouts:
  macos_notification_seconds: 0
  telegram_notification_seconds: 0
  total_timeout_seconds: 60
  timeout_policy: approve
macos:
  phpstorm_bundle_id: "com.jetbrains.phpstorm"
daemon:
  port: 9753
paths:
  claude_settings: "~/.claude/settings.json"
`)
	_, err := config.Load(path)
	if err != nil {
		t.Errorf("both-zero should be valid, got: %v", err)
	}
}

func TestValidationTelegramMustExceedMacosByFive(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 123
timeouts:
  macos_notification_seconds: 15
  telegram_notification_seconds: 18
  total_timeout_seconds: 300
  timeout_policy: deny
macos:
  phpstorm_bundle_id: "com.jetbrains.phpstorm"
daemon:
  port: 9753
paths:
  claude_settings: "~/.claude/settings.json"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Error("expected error when telegram < macos + 5")
	}
}

func TestValidationInvalidTimeoutPolicy(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 123
timeouts:
  macos_notification_seconds: 0
  telegram_notification_seconds: 0
  total_timeout_seconds: 60
  timeout_policy: maybe
macos:
  phpstorm_bundle_id: "com.jetbrains.phpstorm"
daemon:
  port: 9753
paths:
  claude_settings: "~/.claude/settings.json"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Error("expected error for invalid timeout_policy")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path := config.DefaultPath()
	if path == "" {
		t.Error("expected non-empty default path")
	}
}
