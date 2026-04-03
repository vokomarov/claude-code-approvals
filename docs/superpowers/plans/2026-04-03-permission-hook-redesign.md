# Permission Hook Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the broken MCP `permissionPromptTool` approach with a `PermissionRequest` hook that calls a local HTTP daemon.

**Architecture:** Claude Code invokes `claude-code-approvals hook` as a subprocess per permission request. The hook reads stdin JSON, POSTs to the daemon's `/api/permission` endpoint (blocking), and writes the decision as `hookSpecificOutput` JSON to stdout. The daemon holds an `atomic.Bool` enabled flag; when disabled it returns 204 immediately, causing the hook to exit silently and Claude Code to use its built-in interactive prompt.

**Tech Stack:** Go 1.26, standard library only (`net/http`, `sync/atomic`, `encoding/json`) — no new external dependencies added, `mcp-go` removed.

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `internal/settings/settings.go` | Modify | Replace `Enable`/`Disable` with `Install`/`Uninstall` writing hooks entry |
| `internal/settings/settings_test.go` | Modify | Rewrite tests for new `Install`/`Uninstall` API |
| `internal/daemon/server.go` | Modify | Remove MCP wiring; add `atomic.Bool`, `/api/permission`, `/api/enable`, `/api/disable`; extract `handler()` for testability |
| `internal/daemon/server_http_test.go` | Create | HTTP handler tests for new endpoints |
| `internal/hook/hook.go` | Create | Hook subcommand: reads stdin, POSTs to daemon, writes decision to stdout |
| `internal/hook/hook_test.go` | Create | Hook tests using mock HTTP server |
| `cmd/claude-code-approvals/main.go` | Modify | Add `hook`, `install`, `uninstall` subcommands; `on`/`off` become HTTP calls to daemon |
| `internal/mcp/handler.go` | Delete | Logic inlined into `server.go` HTTP handler |
| `internal/mcp/handler_test.go` | Delete | No longer relevant |
| `go.mod` / `go.sum` | Modify | Remove `mcp-go` and transitive deps via `go mod tidy` |

---

## Stage 1 — settings package

### Task 1: Replace `Enable`/`Disable` with `Install`/`Uninstall`

**Files:**
- Modify: `internal/settings/settings.go`
- Modify: `internal/settings/settings_test.go`

- [ ] **Step 1.1: Replace the test file with tests for the new API**

Overwrite `internal/settings/settings_test.go` with:

```go
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
```

- [ ] **Step 1.2: Run tests to verify they fail**

```bash
cd /Users/vovan/go/src/github.com/vokomarov/claude-code-approvals
go test ./internal/settings/... -v -run "TestInstall|TestUninstall|TestMalformed"
```

Expected: FAIL — `settings.Install` and `settings.Uninstall` undefined.

- [ ] **Step 1.3: Rewrite `internal/settings/settings.go`**

Replace the entire file with:

```go
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
```

- [ ] **Step 1.4: Run tests to verify they pass**

```bash
go test ./internal/settings/... -v -race
```

Expected: all PASS.

- [ ] **Step 1.5: Commit**

```bash
git add internal/settings/settings.go internal/settings/settings_test.go
git commit -m "feat(settings): replace Enable/Disable with Install/Uninstall for hook-based approach"
```

---

## Stage 2 — daemon HTTP API

### Task 2: Add `atomic.Bool` enabled flag and new HTTP handlers to `server.go`

**Files:**
- Modify: `internal/daemon/server.go`
- Create: `internal/daemon/server_http_test.go`

- [ ] **Step 2.1: Create `internal/daemon/server_http_test.go` with failing tests**

```go
package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/approvals"
	"github.com/vokomarov/claude-code-approvals/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Telegram: config.Telegram{BotToken: "tok", ChatID: 1},
		Timeouts: config.Timeouts{
			MacosNotificationSeconds:    0,
			TelegramNotificationSeconds: 0,
			TotalTimeoutSeconds:         300,
			TimeoutPolicy:               "deny",
		},
		Daemon: config.Daemon{Port: 0},
		MacOS:  config.MacOS{PhpStormBundleID: "com.test"},
		Paths:  config.Paths{ClaudeSettings: t.TempDir() + "/settings.json"},
	}
}

func TestHealthHandler(t *testing.T) {
	srv, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body)
	}
}

func TestPermissionReturns204WhenDisabled(t *testing.T) {
	srv, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	srv.enabled.Store(false)
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()

	body := `{"tool_name":"Bash","tool_input":{},"session_id":"s1","cwd":"/tmp"}`
	resp, err := http.Post(ts.URL+"/api/permission", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestPermissionBlocksUntilDecision(t *testing.T) {
	srv, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()

	type result struct {
		resp *http.Response
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		body := `{"tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"s1","cwd":"/tmp"}`
		resp, err := http.Post(ts.URL+"/api/permission", "application/json", strings.NewReader(body))
		resultCh <- result{resp, err}
	}()

	// Wait for request to appear in the store, then inject a decision.
	var req *approvals.ApprovalRequest
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if all := srv.store.All(); len(all) > 0 {
			req = all[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if req == nil {
		t.Fatal("request never appeared in store")
	}
	req.Decision <- approvals.Decision{Value: "allow", Source: "test"}

	res := <-resultCh
	if res.err != nil {
		t.Fatal(res.err)
	}
	defer res.resp.Body.Close()
	if res.resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.resp.StatusCode)
	}
	var out map[string]string
	json.NewDecoder(res.resp.Body).Decode(&out)
	if out["decision"] != "allow" {
		t.Errorf("expected allow, got %v", out["decision"])
	}
}

func TestEnableDisableToggle(t *testing.T) {
	srv, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()

	client := &http.Client{}

	// Disable — permission requests should get 204.
	resp, _ := client.Post(ts.URL+"/api/disable", "", nil)
	resp.Body.Close()
	if srv.enabled.Load() {
		t.Error("expected daemon to be disabled")
	}

	body := `{"tool_name":"Bash","tool_input":{},"session_id":"s2","cwd":"/tmp"}`
	resp, _ = http.Post(ts.URL+"/api/permission", "application/json", strings.NewReader(body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 when disabled, got %d", resp.StatusCode)
	}

	// Re-enable.
	resp, _ = client.Post(ts.URL+"/api/enable", "", nil)
	resp.Body.Close()
	if !srv.enabled.Load() {
		t.Error("expected daemon to be enabled")
	}
}
```

- [ ] **Step 2.2: Run tests to verify they fail**

```bash
go test ./internal/daemon/... -v -run "TestHealth|TestPermission|TestEnableDisable"
```

Expected: FAIL — `srv.handler` undefined, `srv.enabled` undefined.

- [ ] **Step 2.3: Rewrite `internal/daemon/server.go`**

Replace the entire file with:

```go
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/approvals"
	"github.com/vokomarov/claude-code-approvals/internal/config"
	"github.com/vokomarov/claude-code-approvals/internal/notifier"
	"github.com/vokomarov/claude-code-approvals/internal/telegram"
)

// Server holds all daemon state.
type Server struct {
	cfg        *config.Config
	store      *approvals.Store
	bot        *telegram.Bot
	httpServer *http.Server
	enabled    atomic.Bool
}

// permissionRequest is the JSON body received at POST /api/permission.
type permissionRequest struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	CWD       string          `json:"cwd"`
}

// New creates a Server. Returns an error if prerequisites fail
// (invalid Telegram token, invalid message template).
// The daemon starts in the enabled state.
func New(cfg *config.Config) (*Server, error) {
	if cfg.Telegram.MessageTemplate != "" {
		if _, err := template.New("").Parse(cfg.Telegram.MessageTemplate); err != nil {
			return nil, fmt.Errorf("invalid message_template: %w", err)
		}
	}

	if cfg.Timeouts.MacosNotificationSeconds > 0 && !notifier.IsAvailable() {
		slog.Warn("terminal-notifier not found; macOS notifications will be skipped")
		cfg.Timeouts.MacosNotificationSeconds = 0
	}

	store := approvals.NewStore()

	var bot *telegram.Bot
	if cfg.Timeouts.TelegramNotificationSeconds > 0 {
		var err error
		bot, err = telegram.NewBot(cfg.Telegram.BotToken, cfg.Telegram.ChatID, cfg.Telegram.MessageTemplate)
		if err != nil {
			return nil, fmt.Errorf("telegram: %w", err)
		}
	}

	s := &Server{cfg: cfg, store: store, bot: bot}
	s.enabled.Store(true)
	return s, nil
}

// handler builds the HTTP mux. Extracted from Run for testability.
// ctx is the daemon lifetime context; it is captured by notification callbacks.
func (s *Server) handler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	mux.HandleFunc("POST /api/enable", func(w http.ResponseWriter, r *http.Request) {
		s.enabled.Store(true)
		slog.Info("daemon enabled")
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /api/disable", func(w http.ResponseWriter, r *http.Request) {
		s.enabled.Store(false)
		slog.Info("daemon disabled")
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /api/permission", func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled.Load() {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var preq permissionRequest
		if err := json.NewDecoder(r.Body).Decode(&preq); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		req := approvals.NewRequest(preq.SessionID, preq.ToolName, string(preq.ToolInput))
		slog.Info("permission request received",
			"id", req.ID, "session", preq.SessionID, "tool", preq.ToolName)

		s.store.Add(req)
		defer func() {
			s.store.Delete(req.ID)
			req.Cancel()
		}()

		approvals.RunMachine(req, approvals.MachineOpts{
			MacosSeconds:    s.cfg.Timeouts.MacosNotificationSeconds,
			TelegramSeconds: s.cfg.Timeouts.TelegramNotificationSeconds,
			TotalSeconds:    s.cfg.Timeouts.TotalTimeoutSeconds,
			TimeoutPolicy:   s.cfg.Timeouts.TimeoutPolicy,
			OnMacos: func(ar *approvals.ApprovalRequest) {
				if s.cfg.Timeouts.MacosNotificationSeconds == 0 {
					return
				}
				title := fmt.Sprintf("Claude Code – %s", ar.ToolName)
				message := notifier.TruncateForMacOS(ar.ToolInput)
				timeoutSecs := s.cfg.Timeouts.TelegramNotificationSeconds - s.cfg.Timeouts.MacosNotificationSeconds
				if timeoutSecs <= 0 {
					timeoutSecs = 30
				}
				go func() {
					result, err := notifier.Notify(ctx, title, message, s.cfg.MacOS.PhpStormBundleID, timeoutSecs)
					if err != nil {
						slog.Warn("terminal-notifier error", "id", ar.ID, "err", err)
						return
					}
					if result == "" {
						return // dismissed without interaction
					}
					decision := "deny"
					if result == "Approve" {
						decision = "allow"
					}
					select {
					case ar.Decision <- approvals.Decision{Value: decision, Source: "macos"}:
						slog.Info("macOS decision received", "id", ar.ID, "decision", decision)
					default:
					}
				}()
			},
			OnTelegram: func(ar *approvals.ApprovalRequest) {
				if s.bot == nil {
					return
				}
				if err := s.bot.SendApprovalRequest(ar); err != nil {
					slog.Error("telegram send failed", "id", ar.ID, "err", err)
				}
			},
		})

		select {
		case decision := <-req.Decision:
			slog.Info("permission decided",
				"id", req.ID, "decision", decision.Value, "source", decision.Source)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"decision":%q}`, decision.Value)
		case <-r.Context().Done():
			slog.Info("http connection dropped", "id", req.ID)
		}
	})

	return mux
}

// Run starts the daemon and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Daemon.Port)

	if s.bot != nil {
		go s.bot.PollForever(ctx, s.store)
	}

	s.httpServer = &http.Server{Addr: addr, Handler: s.handler(ctx)}

	slog.Info("daemon starting", "addr", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		slog.Info("daemon shutting down")
		s.shutdown()
		return nil
	}
}

func (s *Server) shutdown() {
	pending := s.store.All()
	for _, req := range pending {
		select {
		case req.Decision <- approvals.Decision{Value: s.cfg.Timeouts.TimeoutPolicy, Source: "timeout"}:
		default:
		}
		req.Cancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		slog.Warn("http shutdown error", "err", err)
	}
	slog.Info("daemon stopped", "pending_flushed", len(pending))
}
```

- [ ] **Step 2.4: Run tests to verify they pass**

```bash
go test ./internal/daemon/... -v -race
```

Expected: all PASS. If the mcp import in `main.go` causes compile errors, note that main.go still references the old `settings.Enable` — that will be fixed in Task 4. Run only the daemon package for now.

- [ ] **Step 2.5: Commit**

```bash
git add internal/daemon/server.go internal/daemon/server_http_test.go
git commit -m "feat(daemon): replace MCP server with atomic-bool-gated HTTP permission API"
```

---

## Stage 3 — hook subcommand

### Task 3: Implement `internal/hook` package

**Files:**
- Create: `internal/hook/hook.go`
- Create: `internal/hook/hook_test.go`

- [ ] **Step 3.1: Create `internal/hook/hook_test.go`**

```go
package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunWritesAllowDecision(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"decision":"allow"}`)
	}))
	defer ts.Close()

	in := strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"},"cwd":"/tmp"}`)
	var out bytes.Buffer
	run(in, &out, ts.URL, 5*time.Second)

	var got map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, out.String())
	}
	hso, ok := got["hookSpecificOutput"].(map[string]interface{})
	if !ok {
		t.Fatal("hookSpecificOutput missing or not an object")
	}
	if hso["hookEventName"] != "PermissionRequest" {
		t.Errorf("wrong hookEventName: %v", hso["hookEventName"])
	}
	dec, ok := hso["decision"].(map[string]interface{})
	if !ok {
		t.Fatal("decision missing or not an object")
	}
	if dec["behavior"] != "allow" {
		t.Errorf("expected allow, got %v", dec["behavior"])
	}
}

func TestRunWritesDenyDecision(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"decision":"deny"}`)
	}))
	defer ts.Close()

	in := strings.NewReader(`{"session_id":"s1","tool_name":"Write","tool_input":{"path":"/etc/hosts"},"cwd":"/tmp"}`)
	var out bytes.Buffer
	run(in, &out, ts.URL, 5*time.Second)

	var got map[string]interface{}
	json.Unmarshal(out.Bytes(), &got)
	hso := got["hookSpecificOutput"].(map[string]interface{})
	dec := hso["decision"].(map[string]interface{})
	if dec["behavior"] != "deny" {
		t.Errorf("expected deny, got %v", dec["behavior"])
	}
}

func TestRunSilentWhenDaemonDown(t *testing.T) {
	in := strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{},"cwd":"/tmp"}`)
	var out bytes.Buffer
	// Port 1 is privileged — no server will be listening there.
	run(in, &out, "http://localhost:1", 200*time.Millisecond)
	if out.Len() > 0 {
		t.Errorf("expected no output when daemon is down, got: %s", out.String())
	}
}

func TestRunSilentOn204(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	in := strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{},"cwd":"/tmp"}`)
	var out bytes.Buffer
	run(in, &out, ts.URL, 5*time.Second)
	if out.Len() > 0 {
		t.Errorf("expected no output on 204, got: %s", out.String())
	}
}

func TestRunForwardsFullStdinToDaemon(t *testing.T) {
	var received bytes.Buffer
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.ReadFrom(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"decision":"allow"}`)
	}))
	defer ts.Close()

	in := strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls -la"},"cwd":"/home/user"}`)
	var out bytes.Buffer
	run(in, &out, ts.URL, 5*time.Second)

	var body map[string]interface{}
	if err := json.Unmarshal(received.Bytes(), &body); err != nil {
		t.Fatalf("daemon received invalid JSON: %v", err)
	}
	if body["tool_name"] != "Bash" {
		t.Errorf("tool_name not forwarded: %v", body["tool_name"])
	}
	if body["session_id"] != "s1" {
		t.Errorf("session_id not forwarded: %v", body["session_id"])
	}
	if body["cwd"] != "/home/user" {
		t.Errorf("cwd not forwarded: %v", body["cwd"])
	}
}
```

- [ ] **Step 3.2: Run tests to verify they fail**

```bash
go test ./internal/hook/... -v
```

Expected: FAIL — package `hook` does not exist.

- [ ] **Step 3.3: Create `internal/hook/hook.go`**

```go
// Package hook implements the hook subcommand that bridges Claude Code's
// PermissionRequest hook mechanism to the cc-approvals daemon.
package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/config"
)

// hookInput is the JSON received from Claude Code on stdin for a PermissionRequest hook.
type hookInput struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	CWD       string          `json:"cwd"`
}

// daemonResponse is the JSON returned by POST /api/permission.
type daemonResponse struct {
	Decision string `json:"decision"`
}

// Run is the hook subcommand entrypoint. Reads from os.Stdin, writes to os.Stdout.
// Always exits cleanly; errors result in no output, causing Claude Code to fall back
// to its built-in interactive permission prompt.
func Run() {
	port := 9753
	timeout := 310 * time.Second
	if cfg, err := config.Load(config.DefaultPath()); err == nil {
		port = cfg.Daemon.Port
		timeout = time.Duration(cfg.Timeouts.TotalTimeoutSeconds+10) * time.Second
	}
	run(os.Stdin, os.Stdout, fmt.Sprintf("http://localhost:%d", port), timeout)
}

// run is the testable core of the hook subcommand.
func run(in io.Reader, out io.Writer, daemonBaseURL string, clientTimeout time.Duration) {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return
	}

	body, err := json.Marshal(input)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: clientTimeout}
	resp, err := client.Post(daemonBaseURL+"/api/permission", "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var dr daemonResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return
	}

	type decisionOutput struct {
		Behavior string `json:"behavior"`
	}
	type hookSpecificOutput struct {
		HookEventName string         `json:"hookEventName"`
		Decision      decisionOutput `json:"decision"`
	}
	type result struct {
		HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
	}

	output := result{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName: "PermissionRequest",
			Decision:      decisionOutput{Behavior: dr.Decision},
		},
	}
	json.NewEncoder(out).Encode(output)
}
```

- [ ] **Step 3.4: Run tests to verify they pass**

```bash
go test ./internal/hook/... -v -race
```

Expected: all PASS.

- [ ] **Step 3.5: Commit**

```bash
git add internal/hook/hook.go internal/hook/hook_test.go
git commit -m "feat(hook): implement hook subcommand bridging Claude Code PermissionRequest to daemon"
```

---

## Stage 4 — CLI entry point

### Task 4: Update `cmd/claude-code-approvals/main.go`

**Files:**
- Modify: `cmd/claude-code-approvals/main.go`

- [ ] **Step 4.1: Replace `main.go` with the updated version**

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/config"
	"github.com/vokomarov/claude-code-approvals/internal/daemon"
	"github.com/vokomarov/claude-code-approvals/internal/hook"
	"github.com/vokomarov/claude-code-approvals/internal/settings"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "on":
		runOn()
	case "off":
		runOff()
	case "hook":
		hook.Run()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: claude-code-approvals <command>")
	fmt.Fprintln(os.Stderr, "  daemon     Start the approval daemon (used by launchd)")
	fmt.Fprintln(os.Stderr, "  install    One-time: register hook in Claude Code settings.json")
	fmt.Fprintln(os.Stderr, "  uninstall  Remove hook from Claude Code settings.json")
	fmt.Fprintln(os.Stderr, "  on         Enable approval intercepting (daemon must be running)")
	fmt.Fprintln(os.Stderr, "  off        Disable approval intercepting (daemon stays running)")
	fmt.Fprintln(os.Stderr, "  hook       Run as Claude Code PermissionRequest hook (invoked by Claude Code)")
}

func runDaemon() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "path", cfgPath, "err", err)
		os.Exit(1)
	}

	srv, err := daemon.New(cfg)
	if err != nil {
		slog.Error("failed to create daemon", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := srv.Run(ctx); err != nil {
		slog.Error("daemon error", "err", err)
		os.Exit(1)
	}
}

func runInstall() {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	binaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving binary path: %v\n", err)
		os.Exit(1)
	}

	settingsPath := cfg.Paths.ClaudeSettings
	if err := settings.Install(settingsPath, binaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "error updating settings: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Hook installed.")
	fmt.Printf("   Settings updated: %s\n", settingsPath)
	fmt.Println("   This is a one-time setup — run 'on'/'off' to toggle without restarting Claude Code.")
}

func runUninstall() {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	settingsPath := cfg.Paths.ClaudeSettings
	if err := settings.Uninstall(settingsPath); err != nil {
		fmt.Fprintf(os.Stderr, "error updating settings: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Hook uninstalled.")
	fmt.Printf("   Settings updated: %s\n", settingsPath)
}

func runOn() {
	port := loadPort()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(fmt.Sprintf("http://localhost:%d/api/enable", port), "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: daemon not reachable at localhost:%d\n", port)
		fmt.Fprintf(os.Stderr, "       start it with: launchctl start com.vokomarov.cc-approvals\n")
		os.Exit(1)
	}
	resp.Body.Close()
	fmt.Println("✅ Approval intercepting enabled.")
}

func runOff() {
	port := loadPort()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(fmt.Sprintf("http://localhost:%d/api/disable", port), "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: daemon not reachable at localhost:%d\n", port)
		fmt.Fprintf(os.Stderr, "       start it with: launchctl start com.vokomarov.cc-approvals\n")
		os.Exit(1)
	}
	resp.Body.Close()
	fmt.Println("✅ Approval intercepting disabled.")
}

// loadPort returns the daemon port from config, or the default 9753 on error.
func loadPort() int {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return 9753
	}
	return cfg.Daemon.Port
}
```

- [ ] **Step 4.2: Verify the project builds**

```bash
go build ./...
```

Expected: build succeeds. (The mcp package still compiles; it just has no callers now.)

- [ ] **Step 4.3: Run all tests**

```bash
go test ./... -race
```

Expected: all PASS.

- [ ] **Step 4.4: Commit**

```bash
git add cmd/claude-code-approvals/main.go
git commit -m "feat(cli): add install/uninstall/hook subcommands; on/off now toggle daemon state via HTTP"
```

---

## Stage 5 — Remove MCP

### Task 5: Delete `internal/mcp` package and remove `mcp-go` dependency

**Files:**
- Delete: `internal/mcp/handler.go`
- Delete: `internal/mcp/handler_test.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 5.1: Delete the mcp package**

```bash
rm internal/mcp/handler.go internal/mcp/handler_test.go
rmdir internal/mcp
```

- [ ] **Step 5.2: Verify the project still builds**

```bash
go build ./...
```

Expected: build succeeds — no remaining references to `internal/mcp`.

- [ ] **Step 5.3: Remove mcp-go and transitive dependencies**

```bash
go mod tidy
```

Expected: `go.mod` no longer references `github.com/mark3labs/mcp-go` and its transitive-only dependencies (`bahlo/generic-list-go`, `buger/jsonparser`, `google/jsonschema-go`, `invopop/jsonschema`, `mailru/easyjson`, `spf13/cast`, `wk8/go-ordered-map`, `yosida95/uritemplate`) are removed from `go.sum`.

- [ ] **Step 5.4: Run all tests with race detector**

```bash
go test ./... -race
```

Expected: all PASS, no data race warnings.

- [ ] **Step 5.5: Run vet**

```bash
go vet ./...
```

Expected: no warnings.

- [ ] **Step 5.6: Commit**

```bash
git add -A
git commit -m "chore: remove internal/mcp package and mcp-go dependency"
```

---

## Verification Checklist

After all tasks complete, verify end-to-end:

- [ ] `go build ./...` succeeds
- [ ] `go test ./... -race` passes
- [ ] `go vet ./...` clean
- [ ] `claude-code-approvals` prints usage with new subcommands
- [ ] `claude-code-approvals install` writes correct hooks entry to settings.json
- [ ] `claude-code-approvals uninstall` removes the entry
- [ ] `claude-code-approvals off` → daemon returns 204 for permission requests
- [ ] `claude-code-approvals on` → daemon returns decisions for permission requests
- [ ] `go.mod` contains no reference to `mark3labs/mcp-go`
