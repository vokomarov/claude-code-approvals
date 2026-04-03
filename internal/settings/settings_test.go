package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vokomarov/claude-code-approvals/internal/settings"
)

func writeTempSettings(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInstallWritesHookEntry(t *testing.T) {
	path := writeTempSettings(t, `{"theme": "dark"}`)
	if err := settings.Install(path, "/usr/local/bin/claude-code-approvals"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readJSON(t, path)

	hooks, ok := got["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("hooks not a map")
	}
	prHooks, ok := hooks["PermissionRequest"].([]interface{})
	if !ok || len(prHooks) == 0 {
		t.Fatal("PermissionRequest hooks not set")
	}
	entry := prHooks[0].(map[string]interface{})
	innerHooks, ok := entry["hooks"].([]interface{})
	if !ok || len(innerHooks) == 0 {
		t.Fatal("inner hooks array missing")
	}
	cmd := innerHooks[0].(map[string]interface{})
	if cmd["type"] != "command" {
		t.Errorf("expected type=command, got %v", cmd["type"])
	}
	if cmd["command"] != "/usr/local/bin/claude-code-approvals hook" {
		t.Errorf("unexpected command: %v", cmd["command"])
	}
	if got["theme"] != "dark" {
		t.Error("existing keys should be preserved")
	}
}

func TestInstallCreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := settings.Install(path, "/bin/cca"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("settings.json should be created")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	path := writeTempSettings(t, `{}`)
	if err := settings.Install(path, "/bin/cca"); err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	if err := settings.Install(path, "/bin/cca"); err != nil {
		t.Fatalf("second install failed: %v", err)
	}
	got := readJSON(t, path)
	hooks := got["hooks"].(map[string]interface{})
	prHooks := hooks["PermissionRequest"].([]interface{})
	if len(prHooks) != 1 {
		t.Errorf("expected exactly 1 entry, got %d", len(prHooks))
	}
}

func TestUninstallRemovesHookEntry(t *testing.T) {
	path := writeTempSettings(t, `{
		"theme": "dark",
		"hooks": {
			"PermissionRequest": [{"hooks":[{"type":"command","command":"/bin/cca hook"}]}]
		}
	}`)
	if err := settings.Uninstall(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readJSON(t, path)
	if _, exists := got["hooks"]; exists {
		t.Error("hooks key should be removed when empty")
	}
	if got["theme"] != "dark" {
		t.Error("existing keys should be preserved")
	}
}

func TestUninstallKeepsOtherHooks(t *testing.T) {
	path := writeTempSettings(t, `{
		"hooks": {
			"PermissionRequest": [{"hooks":[{"type":"command","command":"/bin/cca hook"}]}],
			"PreToolUse": [{"hooks":[{"type":"command","command":"/other/hook"}]}]
		}
	}`)
	if err := settings.Uninstall(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readJSON(t, path)
	hooks, ok := got["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("hooks key should still exist")
	}
	if _, exists := hooks["PermissionRequest"]; exists {
		t.Error("PermissionRequest should be removed")
	}
	if _, exists := hooks["PreToolUse"]; !exists {
		t.Error("PreToolUse should be preserved")
	}
}

func TestUninstallNoOpIfFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := settings.Uninstall(path); err != nil {
		t.Errorf("expected no error for missing file, got: %v", err)
	}
}

func TestMalformedJSONAbortsInstall(t *testing.T) {
	path := writeTempSettings(t, `{bad json}`)
	err := settings.Install(path, "/bin/cca")
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{bad json}` {
		t.Error("original file should not be modified on error")
	}
}

func TestMalformedJSONAbortsUninstall(t *testing.T) {
	path := writeTempSettings(t, `{bad json}`)
	err := settings.Uninstall(path)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{bad json}` {
		t.Error("original file should not be modified on error")
	}
}
