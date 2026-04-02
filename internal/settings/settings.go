package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	permissionPromptToolKey   = "permissionPromptTool"
	permissionPromptToolValue = "mcp__cc-approvals__request_permission"
	mcpServersKey             = "mcpServers"
	serverName                = "cc-approvals"
)

// Enable injects the cc-approvals MCP server and permissionPromptTool into settings.json.
// Creates the file if it does not exist. Atomic write via temp file + rename.
func Enable(path string, port int) error {
	data, err := readOrEmpty(path)
	if err != nil {
		return err
	}

	var m map[string]interface{}
	if len(data) == 0 {
		m = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("malformed settings.json: %w", err)
		}
	}

	// Inject mcpServers.cc-approvals
	servers, _ := m[mcpServersKey].(map[string]interface{})
	if servers == nil {
		servers = make(map[string]interface{})
	}
	servers[serverName] = map[string]interface{}{
		"type": "sse",
		"url":  fmt.Sprintf("http://localhost:%d/mcp", port),
	}
	m[mcpServersKey] = servers
	m[permissionPromptToolKey] = permissionPromptToolValue

	return writeAtomic(path, m)
}

// Disable removes the cc-approvals MCP server and permissionPromptTool from settings.json.
func Disable(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to do
		}
		return err
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("malformed settings.json: %w", err)
	}

	delete(m, permissionPromptToolKey)

	if servers, ok := m[mcpServersKey].(map[string]interface{}); ok {
		delete(servers, serverName)
		if len(servers) == 0 {
			delete(m, mcpServersKey)
		}
	}

	return writeAtomic(path, m)
}

func readOrEmpty(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func writeAtomic(path string, m map[string]interface{}) error {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".settings-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
