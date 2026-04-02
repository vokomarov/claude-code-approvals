package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// minTelegramBuffer is the minimum seconds between macOS and Telegram notification timeouts,
// ensuring the macOS notification is visible before it auto-dismisses.
const minTelegramBuffer = 5

type Telegram struct {
	BotToken        string `yaml:"bot_token"`
	ChatID          int64  `yaml:"chat_id"`
	MessageTemplate string `yaml:"message_template"`
}

type Timeouts struct {
	MacosNotificationSeconds    int    `yaml:"macos_notification_seconds"`
	TelegramNotificationSeconds int    `yaml:"telegram_notification_seconds"`
	TotalTimeoutSeconds         int    `yaml:"total_timeout_seconds"`
	TimeoutPolicy               string `yaml:"timeout_policy"`
}

type MacOS struct {
	PhpStormBundleID string `yaml:"phpstorm_bundle_id"`
}

type Daemon struct {
	Port int `yaml:"port"`
}

type Paths struct {
	ClaudeSettings string `yaml:"claude_settings"`
}

type Config struct {
	Telegram Telegram `yaml:"telegram"`
	Timeouts Timeouts `yaml:"timeouts"`
	MacOS    MacOS    `yaml:"macos"`
	Daemon   Daemon   `yaml:"daemon"`
	Paths    Paths    `yaml:"paths"`
}

func DefaultPath() string {
	// UserHomeDir rarely fails; fallback to relative path if it does
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cc-approvals", "config.yaml")
}

func Load(path string) (*Config, error) {
	var err error
	path, err = expandTilde(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	cfg.Paths.ClaudeSettings, err = expandTilde(cfg.Paths.ClaudeSettings)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.Telegram.BotToken == "" {
		return fmt.Errorf("telegram.bot_token is required")
	}
	if cfg.Telegram.ChatID == 0 {
		return fmt.Errorf("telegram.chat_id is required")
	}
	if cfg.Timeouts.TimeoutPolicy != "deny" && cfg.Timeouts.TimeoutPolicy != "approve" {
		return fmt.Errorf("timeouts.timeout_policy must be 'deny' or 'approve', got %q", cfg.Timeouts.TimeoutPolicy)
	}
	m := cfg.Timeouts.MacosNotificationSeconds
	tg := cfg.Timeouts.TelegramNotificationSeconds
	total := cfg.Timeouts.TotalTimeoutSeconds
	if m < 0 || tg < 0 {
		return fmt.Errorf("notification timeouts must be >= 0")
	}
	if m > 0 && tg > 0 && tg < m+minTelegramBuffer {
		return fmt.Errorf("telegram_notification_seconds (%d) must exceed macos_notification_seconds (%d) by at least 5", tg, m)
	}
	maxNotification := m
	if tg > maxNotification {
		maxNotification = tg
	}
	if total <= 0 || (maxNotification > 0 && total <= maxNotification) {
		return fmt.Errorf("total_timeout_seconds (%d) must be > 0 and greater than the largest notification timeout (%d)", total, maxNotification)
	}
	if cfg.Daemon.Port < 1 || cfg.Daemon.Port > 65535 {
		return fmt.Errorf("daemon.port must be between 1 and 65535")
	}
	if cfg.Paths.ClaudeSettings == "" {
		return fmt.Errorf("paths.claude_settings is required")
	}
	return nil
}

func expandTilde(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expanding tilde: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
