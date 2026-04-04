package config_test

import (
	"os"
	"path/filepath"
	"strings"
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
	want := filepath.Join(".config", "cc-approvals", "config.yaml")
	if !strings.HasSuffix(path, want) {
		t.Errorf("expected path ending in %q, got %q", want, path)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	path := writeTempConfig(t, `{not valid yaml: [}`)
	_, err := config.Load(path)
	if err == nil {
		t.Error("expected error for malformed YAML")
	}
}

func TestValidateTotalZeroIsValid(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 1
timeouts:
  macos_notification_seconds: 0
  telegram_notification_seconds: 0
  total_timeout_seconds: 0
  timeout_policy: ""
daemon:
  port: 9753
paths:
  claude_settings: "/tmp/settings.json"
`)
	_, err := config.Load(path)
	if err != nil {
		t.Errorf("expected no error for total=0, got: %v", err)
	}
}

func TestValidateTotalZeroWithNotificationsIsValid(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 1
timeouts:
  macos_notification_seconds: 15
  telegram_notification_seconds: 30
  total_timeout_seconds: 0
  timeout_policy: ""
daemon:
  port: 9753
paths:
  claude_settings: "/tmp/settings.json"
`)
	_, err := config.Load(path)
	if err != nil {
		t.Errorf("expected no error for total=0 with notifications, got: %v", err)
	}
}

func TestValidateTotalPositiveStillRequiresPolicy(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 1
timeouts:
  macos_notification_seconds: 0
  telegram_notification_seconds: 0
  total_timeout_seconds: 60
  timeout_policy: ""
daemon:
  port: 9753
paths:
  claude_settings: "/tmp/settings.json"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Error("expected error when total>0 and timeout_policy is empty")
	}
}

func TestValidateTotalPositiveStillRequiresExceedNotification(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 1
timeouts:
  macos_notification_seconds: 15
  telegram_notification_seconds: 30
  total_timeout_seconds: 20
  timeout_policy: deny
daemon:
  port: 9753
paths:
  claude_settings: "/tmp/settings.json"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Error("expected error when total <= max notification timeout")
	}
}

func TestValidationMissingClaudeSettings(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 123
timeouts:
  macos_notification_seconds: 0
  telegram_notification_seconds: 0
  total_timeout_seconds: 60
  timeout_policy: deny
macos:
  phpstorm_bundle_id: "com.jetbrains.phpstorm"
daemon:
  port: 9753
paths:
  claude_settings: ""
`)
	_, err := config.Load(path)
	if err == nil {
		t.Error("expected error for empty claude_settings")
	}
}
