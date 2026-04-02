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

func TestEnableInjectsKeys(t *testing.T) {
	path := writeTempSettings(t, `{"theme": "dark"}`)
	if err := settings.Enable(path, 9753); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readJSON(t, path)
	if got["permissionPromptTool"] != "mcp__cc-approvals__request_permission" {
		t.Errorf("permissionPromptTool not set: %v", got["permissionPromptTool"])
	}
	servers, ok := got["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("mcpServers not a map")
	}
	if _, exists := servers["cc-approvals"]; !exists {
		t.Error("cc-approvals not in mcpServers")
	}
	if got["theme"] != "dark" {
		t.Error("existing keys should be preserved")
	}
}

func TestDisableRemovesKeys(t *testing.T) {
	path := writeTempSettings(t, `{
        "theme": "dark",
        "permissionPromptTool": "mcp__cc-approvals__request_permission",
        "mcpServers": {"cc-approvals": {"type": "sse", "url": "http://localhost:9753/mcp"}}
    }`)
	if err := settings.Disable(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readJSON(t, path)
	if _, exists := got["permissionPromptTool"]; exists {
		t.Error("permissionPromptTool should have been removed")
	}
	if _, exists := got["mcpServers"]; exists {
		t.Error("mcpServers should have been removed when cc-approvals was the only entry")
	}
	if got["theme"] != "dark" {
		t.Error("existing keys should be preserved")
	}
}

func TestDisableKeepsOtherMCPServers(t *testing.T) {
	path := writeTempSettings(t, `{
        "mcpServers": {
            "cc-approvals": {"type": "sse"},
            "other-server": {"type": "stdio"}
        },
        "permissionPromptTool": "mcp__cc-approvals__request_permission"
    }`)
	if err := settings.Disable(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readJSON(t, path)
	servers := got["mcpServers"].(map[string]interface{})
	if _, exists := servers["cc-approvals"]; exists {
		t.Error("cc-approvals should be removed")
	}
	if _, exists := servers["other-server"]; !exists {
		t.Error("other-server should be preserved")
	}
}

func TestMalformedJSONAborts(t *testing.T) {
	path := writeTempSettings(t, `{bad json}`)
	err := settings.Enable(path, 9753)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
	// Original file should be untouched
	data, _ := os.ReadFile(path)
	if string(data) != `{bad json}` {
		t.Error("original file should not be modified on error")
	}
}

func TestEnableCreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := settings.Enable(path, 9753); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("settings.json should be created")
	}
}
