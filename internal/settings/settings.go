package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	hooksKey             = "hooks"
	permissionRequestKey = "PermissionRequest"
)

// Install writes the PermissionRequest hook entry into settings.json.
// binaryPath is the absolute path to the claude-code-approvals binary.
// Creates the file if it does not exist. Atomic write via temp file + rename.
// Existing settings.json keys are preserved.
func Install(path, binaryPath string) error {
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

	hookEntry := []interface{}{
		map[string]interface{}{
			"hooks": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": binaryPath + " hook",
				},
			},
		},
	}

	hooks, _ := m[hooksKey].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}
	hooks[permissionRequestKey] = hookEntry
	m[hooksKey] = hooks

	return writeAtomic(path, m)
}

// Uninstall removes the PermissionRequest hook entry from settings.json.
// No-op if the file does not exist.
func Uninstall(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("malformed settings.json: %w", err)
	}

	if hooks, ok := m[hooksKey].(map[string]interface{}); ok {
		delete(hooks, permissionRequestKey)
		if len(hooks) == 0 {
			delete(m, hooksKey)
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
