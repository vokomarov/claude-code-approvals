# Claude Code Mobile Approvals — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go daemon that intercepts Claude Code permission requests and escalates them to macOS notifications and/or a Telegram bot when not answered locally, with `cc on`/`cc off` commands to toggle the system.

**Architecture:** Single `cc` binary with `daemon`, `on`, and `off` subcommands. The daemon serves an MCP SSE server (for Claude Code) and an HTTP health endpoint on port 9753. Each incoming `request_permission` call drives a two-timer state machine (macOS notification at t=Ns, Telegram at t=Ns) backed by three competing goroutines sending to a capacity-1 channel.

**Tech Stack:** Go 1.21+ (stdlib concurrency, `log/slog`), `github.com/mark3labs/mcp-go` (MCP SSE server), `github.com/go-telegram-bot-api/telegram-bot-api/v5` (Telegram), `gopkg.in/yaml.v3` (config), `terminal-notifier` (macOS notifications, external binary).

---

## File Map

| File | Responsibility |
|---|---|
| `cmd/cc/main.go` | CLI entrypoint; routes `daemon`, `on`, `off` subcommands |
| `internal/config/config.go` | Load, expand, and validate `~/.config/cc-approvals/config.yaml` |
| `internal/approvals/types.go` | `ApprovalRequest`, `Decision` types |
| `internal/approvals/store.go` | Thread-safe store: add, get, delete by UUID |
| `internal/approvals/machine.go` | Per-request state machine: timer goroutines, Decision channel |
| `internal/notifier/macos.go` | Spawn/kill `terminal-notifier` subprocess; read stdout for decision |
| `internal/telegram/template.go` | Render message from Go template + truncate ToolInput |
| `internal/telegram/bot.go` | Long-poll loop; send approval message; route callback to store |
| `internal/mcp/handler.go` | Register `request_permission` tool; block until Decision; return to Claude Code |
| `internal/daemon/server.go` | Wire MCP SSE server + HTTP health; accept connections; graceful shutdown |
| `internal/settings/settings.go` | Atomic read/write of Claude Code `settings.json`; inject/remove keys |
| `config.example.yaml` | Documented example config |
| `launchd/com.vokomarov.cc-approvals.plist` | launchd service definition |

---

## Task 1: Project Scaffold

**Files:**
- Create: `~/go/src/github.com/vokomarov/claude-code-approvals/go.mod`
- Create: `cmd/cc/main.go`
- Create: `internal/config/config.go`
- Create: `internal/approvals/types.go`
- Create: `internal/approvals/store.go`
- Create: `internal/approvals/machine.go`
- Create: `internal/notifier/macos.go`
- Create: `internal/telegram/template.go`
- Create: `internal/telegram/bot.go`
- Create: `internal/mcp/handler.go`
- Create: `internal/daemon/server.go`
- Create: `internal/settings/settings.go`

- [ ] **Step 1: Create the repository directory and Go module**

```bash
mkdir -p ~/go/src/github.com/vokomarov/claude-code-approvals
cd ~/go/src/github.com/vokomarov/claude-code-approvals
go mod init github.com/vokomarov/claude-code-approvals
```

Expected: `go.mod` created with `module github.com/vokomarov/claude-code-approvals` and a `go 1.21` (or later) directive.

- [ ] **Step 2: Add dependencies**

```bash
go get github.com/mark3labs/mcp-go@latest
go get github.com/go-telegram-bot-api/telegram-bot-api/v5@latest
go get gopkg.in/yaml.v3@latest
```

Expected: `go.sum` and `go.mod` updated with the three dependencies.

- [ ] **Step 3: Create all package stub files**

Create each file listed above as a minimal valid Go file (package declaration only). This makes the project compile from the start.

```bash
mkdir -p cmd/cc internal/config internal/approvals internal/notifier internal/telegram internal/mcp internal/daemon internal/settings

echo 'package main\n\nfunc main() {}' > cmd/cc/main.go
echo 'package config' > internal/config/config.go
echo 'package approvals' > internal/approvals/types.go
echo 'package approvals' > internal/approvals/store.go
echo 'package approvals' > internal/approvals/machine.go
echo 'package notifier' > internal/notifier/macos.go
echo 'package telegram' > internal/telegram/template.go
echo 'package telegram' > internal/telegram/bot.go
echo 'package mcp' > internal/mcp/handler.go
echo 'package daemon' > internal/daemon/server.go
echo 'package settings' > internal/settings/settings.go
```

- [ ] **Step 4: Verify the project builds**

```bash
go build ./...
```

Expected: No output (success). Fix any syntax errors.

- [ ] **Step 5: Commit**

```bash
git init
git add .
git commit -m "feat: scaffold project and add dependencies"
```

---

## Task 2: Config Package

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `config.example.yaml`

- [ ] **Step 1: Write the failing tests**

Create `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/config/... -v
```

Expected: compilation failure (functions not defined yet).

- [ ] **Step 3: Implement `internal/config/config.go`**

```go
package config

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "gopkg.in/yaml.v3"
)

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
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".config", "cc-approvals", "config.yaml")
}

func Load(path string) (*Config, error) {
    path = expandTilde(path)
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
    // Expand tilde in paths
    cfg.Paths.ClaudeSettings = expandTilde(cfg.Paths.ClaudeSettings)
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
    if m > 0 && tg > 0 && tg < m+5 {
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
    return nil
}

func expandTilde(path string) string {
    if strings.HasPrefix(path, "~/") {
        home, _ := os.UserHomeDir()
        return filepath.Join(home, path[2:])
    }
    return path
}
```

- [ ] **Step 4: Create `config.example.yaml`**

```yaml
telegram:
  bot_token: "YOUR_BOT_TOKEN_FROM_BOTFATHER"
  chat_id: 123456789
  message_template: ""  # leave empty for default; Go text/template syntax

timeouts:
  macos_notification_seconds: 15   # 0 = skip macOS notification entirely
  telegram_notification_seconds: 30 # 0 = skip Telegram notification entirely
  total_timeout_seconds: 300        # hard ceiling; timeout_policy applied after this
  timeout_policy: deny              # deny | approve

macos:
  phpstorm_bundle_id: "com.jetbrains.phpstorm"

daemon:
  port: 9753

paths:
  claude_settings: "~/.dotfiles/config/claude/settings.json"
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/config/... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add .
git commit -m "feat: config loading and validation"
```

---

## Task 3: Core Types and Approval Store

**Files:**
- Modify: `internal/approvals/types.go`
- Modify: `internal/approvals/store.go`
- Create: `internal/approvals/store_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/approvals/store_test.go`:

```go
package approvals_test

import (
    "sync"
    "testing"
    "time"

    "github.com/vokomarov/claude-code-approvals/internal/approvals"
)

func TestStoreAddAndGet(t *testing.T) {
    store := approvals.NewStore()
    req := approvals.NewRequest("sess1", "Bash", `{"command":"ls"}`)

    store.Add(req)
    got, ok := store.Get(req.ID)
    if !ok {
        t.Fatal("expected to find request")
    }
    if got.ID != req.ID {
        t.Errorf("got ID %q, want %q", got.ID, req.ID)
    }
}

func TestStoreDelete(t *testing.T) {
    store := approvals.NewStore()
    req := approvals.NewRequest("sess1", "Bash", `{"command":"ls"}`)
    store.Add(req)
    store.Delete(req.ID)
    _, ok := store.Get(req.ID)
    if ok {
        t.Error("expected request to be deleted")
    }
}

func TestStoreUniqueIDs(t *testing.T) {
    store := approvals.NewStore()
    r1 := approvals.NewRequest("s", "Bash", "{}")
    r2 := approvals.NewRequest("s", "Bash", "{}")
    store.Add(r1)
    store.Add(r2)
    if r1.ID == r2.ID {
        t.Error("expected unique IDs")
    }
}

func TestStoreConcurrentAccess(t *testing.T) {
    store := approvals.NewStore()
    var wg sync.WaitGroup
    for i := 0; i < 50; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            req := approvals.NewRequest("s", "Bash", "{}")
            store.Add(req)
            store.Get(req.ID)
            store.Delete(req.ID)
        }()
    }
    wg.Wait()
}

func TestRequestDecisionChannel(t *testing.T) {
    req := approvals.NewRequest("s", "Bash", "{}")
    if cap(req.Decision) != 1 {
        t.Errorf("Decision channel should have capacity 1, got %d", cap(req.Decision))
    }
    req.Decision <- approvals.Decision{Value: "allow", Source: "test"}
    select {
    case d := <-req.Decision:
        if d.Value != "allow" {
            t.Errorf("expected allow, got %q", d.Value)
        }
    case <-time.After(time.Second):
        t.Error("timeout reading decision")
    }
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/approvals/... -v
```

Expected: compile error (types not defined).

- [ ] **Step 3: Implement `internal/approvals/types.go`**

```go
package approvals

import (
    "context"
    "time"
)

// Decision is the outcome of an approval request.
type Decision struct {
    Value  string // "allow" | "deny"
    Source string // "macos" | "telegram" | "timeout"
}

// ApprovalRequest holds all state for a single pending permission request.
type ApprovalRequest struct {
    ID          string
    SessionID   string
    ToolName    string
    ToolInput   string // raw JSON from Claude Code
    ProjectPath string // daemon CWD at startup; may be empty
    CreatedAt   time.Time
    Decision    chan Decision // capacity 1; first write wins
    Cancel      context.CancelFunc
}
```

- [ ] **Step 4: Implement `internal/approvals/store.go`**

```go
package approvals

import (
    "context"
    "sync"
    "time"

    "github.com/google/uuid"
)

// Note: uuid is in stdlib alternative — use crypto/rand instead to avoid extra dep.
// We'll generate UUIDs manually.

import (
    "context"
    "crypto/rand"
    "fmt"
    "sync"
    "time"
)

// NewRequest creates a new ApprovalRequest with a unique UUID and a ready Decision channel.
func NewRequest(sessionID, toolName, toolInput string) *ApprovalRequest {
    ctx, cancel := context.WithCancel(context.Background())
    _ = ctx // context used by machine.go goroutines
    return &ApprovalRequest{
        ID:        newUUID(),
        SessionID: sessionID,
        ToolName:  toolName,
        ToolInput: toolInput,
        CreatedAt: time.Now(),
        Decision:  make(chan Decision, 1),
        Cancel:    cancel,
    }
}

func newUUID() string {
    b := make([]byte, 16)
    _, _ = rand.Read(b)
    return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
        b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Store is a thread-safe map of pending ApprovalRequests keyed by UUID.
type Store struct {
    mu   sync.RWMutex
    reqs map[string]*ApprovalRequest
}

func NewStore() *Store {
    return &Store{reqs: make(map[string]*ApprovalRequest)}
}

func (s *Store) Add(req *ApprovalRequest) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.reqs[req.ID] = req
}

func (s *Store) Get(id string) (*ApprovalRequest, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    req, ok := s.reqs[id]
    return req, ok
}

func (s *Store) Delete(id string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    delete(s.reqs, id)
}

// All returns a snapshot of all pending requests (used during shutdown).
func (s *Store) All() []*ApprovalRequest {
    s.mu.RLock()
    defer s.mu.RUnlock()
    out := make([]*ApprovalRequest, 0, len(s.reqs))
    for _, req := range s.reqs {
        out = append(out, req)
    }
    return out
}
```

**Note:** The `NewRequest` function above has a duplicate import block for clarity. In the actual file, consolidate into a single import block. Also, the `ctx` from `context.WithCancel` should be stored or passed to the machine goroutines — see Task 4.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/approvals/... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add .
git commit -m "feat: approval request types and thread-safe store"
```

---

## Task 4: State Machine (Timer Goroutines)

**Files:**
- Modify: `internal/approvals/machine.go`
- Create: `internal/approvals/machine_test.go`

The machine runs up to three goroutines per request:
- Goroutine A: fires at `macos_notification_seconds`, spawns macOS notification (skipped if 0)
- Goroutine B: fires at `telegram_notification_seconds`, sends Telegram (skipped if 0)
- Goroutine C: fires at `total_timeout_seconds`, writes timeout_policy to Decision channel

All three are started together; the first write to the buffered Decision channel wins. Context cancellation stops all three.

- [ ] **Step 1: Write the failing tests**

Create `internal/approvals/machine_test.go`:

```go
package approvals_test

import (
    "testing"
    "time"

    "github.com/vokomarov/claude-code-approvals/internal/approvals"
)

type machineConfig struct {
    MacosSecs    int
    TelegramSecs int
    TotalSecs    int
    Policy       string
}

func TestMachineTimeoutPolicy(t *testing.T) {
    req := approvals.NewRequest("s", "Bash", "{}")
    cfg := machineConfig{MacosSecs: 0, TelegramSecs: 0, TotalSecs: 1, Policy: "deny"}

    var macosCount, telegramCount int
    onMacos := func(r *approvals.ApprovalRequest) {}
    onTelegram := func(r *approvals.ApprovalRequest) {}

    approvals.RunMachine(req, approvals.MachineOpts{
        MacosSeconds:    cfg.MacosSecs,
        TelegramSeconds: cfg.TelegramSecs,
        TotalSeconds:    cfg.TotalSecs,
        TimeoutPolicy:   cfg.Policy,
        OnMacos:         onMacos,
        OnTelegram:      onTelegram,
    })

    select {
    case d := <-req.Decision:
        if d.Value != "deny" {
            t.Errorf("expected deny from timeout, got %q", d.Value)
        }
        if d.Source != "timeout" {
            t.Errorf("expected source=timeout, got %q", d.Source)
        }
        _ = macosCount
        _ = telegramCount
    case <-time.After(3 * time.Second):
        t.Error("timeout: machine did not produce decision")
    }
}

func TestMachineMacosCallbackFires(t *testing.T) {
    req := approvals.NewRequest("s", "Bash", "{}")
    fired := make(chan struct{}, 1)

    approvals.RunMachine(req, approvals.MachineOpts{
        MacosSeconds:    1,
        TelegramSeconds: 10,
        TotalSeconds:    30,
        TimeoutPolicy:   "deny",
        OnMacos:         func(r *approvals.ApprovalRequest) { fired <- struct{}{} },
        OnTelegram:      func(r *approvals.ApprovalRequest) {},
    })

    select {
    case <-fired:
        // success
    case <-time.After(3 * time.Second):
        t.Error("OnMacos was not called within expected time")
    }
    req.Cancel()
}

func TestMachineTelegramCallbackFires(t *testing.T) {
    req := approvals.NewRequest("s", "Bash", "{}")
    fired := make(chan struct{}, 1)

    approvals.RunMachine(req, approvals.MachineOpts{
        MacosSeconds:    0,
        TelegramSeconds: 1,
        TotalSeconds:    30,
        TimeoutPolicy:   "deny",
        OnMacos:         func(r *approvals.ApprovalRequest) {},
        OnTelegram:      func(r *approvals.ApprovalRequest) { fired <- struct{}{} },
    })

    select {
    case <-fired:
        // success
    case <-time.After(3 * time.Second):
        t.Error("OnTelegram was not called within expected time")
    }
    req.Cancel()
}

func TestMachineFirstWriteWins(t *testing.T) {
    req := approvals.NewRequest("s", "Bash", "{}")

    approvals.RunMachine(req, approvals.MachineOpts{
        MacosSeconds:    0,
        TelegramSeconds: 0,
        TotalSeconds:    60,
        TimeoutPolicy:   "deny",
        OnMacos:         func(r *approvals.ApprovalRequest) {},
        OnTelegram:      func(r *approvals.ApprovalRequest) {},
    })

    // External decision arrives (e.g. from macOS notifier)
    req.Decision <- approvals.Decision{Value: "allow", Source: "macos"}

    select {
    case d := <-req.Decision:
        if d.Value != "allow" {
            t.Errorf("expected allow, got %q", d.Value)
        }
    case <-time.After(time.Second):
        t.Error("timeout reading decision")
    }
    req.Cancel()
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/approvals/... -run TestMachine -v
```

Expected: compile error.

- [ ] **Step 3: Implement `internal/approvals/machine.go`**

```go
package approvals

import (
    "context"
    "time"
)

// MachineOpts configures the state machine for a single request.
type MachineOpts struct {
    MacosSeconds    int    // 0 = skip macOS notification
    TelegramSeconds int    // 0 = skip Telegram notification
    TotalSeconds    int    // hard ceiling
    TimeoutPolicy   string // "allow" | "deny"
    OnMacos         func(*ApprovalRequest)
    OnTelegram      func(*ApprovalRequest)
}

// RunMachine starts the background goroutines for a request.
// It returns immediately; goroutines run until the context is cancelled
// or a decision is written to req.Decision.
func RunMachine(req *ApprovalRequest, opts MachineOpts) {
    ctx, cancel := context.WithCancel(context.Background())
    // Wrap the request's own cancel so both can stop the machine.
    origCancel := req.Cancel
    req.Cancel = func() {
        cancel()
        origCancel()
    }

    // Goroutine: macOS notification timer
    if opts.MacosSeconds > 0 {
        go func() {
            select {
            case <-time.After(time.Duration(opts.MacosSeconds) * time.Second):
                opts.OnMacos(req)
            case <-ctx.Done():
            }
        }()
    }

    // Goroutine: Telegram notification timer
    if opts.TelegramSeconds > 0 {
        go func() {
            select {
            case <-time.After(time.Duration(opts.TelegramSeconds) * time.Second):
                opts.OnTelegram(req)
            case <-ctx.Done():
            }
        }()
    }

    // Goroutine: total timeout (always runs)
    go func() {
        select {
        case <-time.After(time.Duration(opts.TotalSeconds) * time.Second):
            // Non-blocking send: if channel already has a value, this is a no-op.
            select {
            case req.Decision <- Decision{Value: opts.TimeoutPolicy, Source: "timeout"}:
            default:
            }
            cancel()
        case <-ctx.Done():
        }
    }()

    // Goroutine: watch for any decision and cancel the context to stop all others.
    go func() {
        select {
        case d := <-req.Decision:
            // Put it back so the caller can read it, then cancel.
            req.Decision <- d
            cancel()
        case <-ctx.Done():
        }
    }()
}
```

**Note:** The "watch and put back" pattern has a race. The correct pattern is to use a separate done channel. Revise as follows — replace the last goroutine and use a dedicated stop mechanism:

```go
// RunMachine starts background goroutines. Call req.Cancel() to stop them all.
// When a decision is written to req.Decision (capacity 1), all goroutines stop.
func RunMachine(req *ApprovalRequest, opts MachineOpts) {
    ctx, cancel := context.WithCancel(context.Background())
    origCancel := req.Cancel
    req.Cancel = func() { cancel(); origCancel() }

    startTimer := func(seconds int, cb func(*ApprovalRequest)) {
        go func() {
            t := time.NewTimer(time.Duration(seconds) * time.Second)
            defer t.Stop()
            select {
            case <-t.C:
                cb(req)
            case <-ctx.Done():
            }
        }()
    }

    if opts.MacosSeconds > 0 {
        startTimer(opts.MacosSeconds, opts.OnMacos)
    }
    if opts.TelegramSeconds > 0 {
        startTimer(opts.TelegramSeconds, opts.OnTelegram)
    }

    // Total timeout always runs and writes to Decision directly.
    go func() {
        t := time.NewTimer(time.Duration(opts.TotalSeconds) * time.Second)
        defer t.Stop()
        select {
        case <-t.C:
            select {
            case req.Decision <- Decision{Value: opts.TimeoutPolicy, Source: "timeout"}:
            default: // already decided
            }
            cancel()
        case <-ctx.Done():
        }
    }()
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/approvals/... -v -timeout 30s
```

Expected: all tests PASS (timer tests use 1s sleeps).

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: state machine with configurable timers and first-write-wins decision"
```

---

## Task 5: macOS Notifier

**Files:**
- Modify: `internal/notifier/macos.go`
- Create: `internal/notifier/macos_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/notifier/macos_test.go`:

```go
package notifier_test

import (
    "testing"

    "github.com/vokomarov/claude-code-approvals/internal/notifier"
)

func TestTruncateToolInput(t *testing.T) {
    long := string(make([]byte, 300))
    for i := range long {
        long = long[:i] + "a" + long[i+1:]
    }
    result := notifier.TruncateForMacOS(long)
    if len(result) > 200 {
        t.Errorf("expected <= 200 chars, got %d", len(result))
    }
    if result[len(result)-3:] != "..." {
        t.Error("expected truncated string to end with '...'")
    }
}

func TestTruncateShortInput(t *testing.T) {
    short := "ls -la"
    result := notifier.TruncateForMacOS(short)
    if result != short {
        t.Errorf("short input should not be modified, got %q", result)
    }
}

func TestNotifierAvailable(t *testing.T) {
    // Just checks the availability check doesn't panic.
    _ = notifier.IsAvailable()
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/notifier/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement `internal/notifier/macos.go`**

```go
package notifier

import (
    "bufio"
    "context"
    "fmt"
    "log/slog"
    "os/exec"
    "strings"
)

const maxMacOSMessageLen = 200

// TruncateForMacOS truncates a string to 200 characters for the macOS notification message.
func TruncateForMacOS(s string) string {
    if len(s) <= maxMacOSMessageLen {
        return s
    }
    return s[:197] + "..."
}

// IsAvailable returns true if terminal-notifier is on PATH.
func IsAvailable() bool {
    _, err := exec.LookPath("terminal-notifier")
    return err == nil
}

// Notify sends a macOS notification with Approve/Deny buttons and returns the
// button pressed ("Approve" or "Deny"), or an empty string if dismissed without interaction.
// It blocks until the notification is interacted with or the context is cancelled.
func Notify(ctx context.Context, title, message, phpstormBundleID string, timeoutSeconds int) (string, error) {
    args := []string{
        "-title", title,
        "-message", message,
        "-actions", "Approve,Deny",
        "-activate", phpstormBundleID,
        "-timeout", fmt.Sprintf("%d", timeoutSeconds),
    }
    cmd := exec.CommandContext(ctx, "terminal-notifier", args...)

    stdout, err := cmd.StdoutPipe()
    if err != nil {
        return "", fmt.Errorf("stdout pipe: %w", err)
    }
    if err := cmd.Start(); err != nil {
        return "", fmt.Errorf("start terminal-notifier: %w", err)
    }

    var result string
    scanner := bufio.NewScanner(stdout)
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if line == "Approve" || line == "Deny" {
            result = line
        }
    }

    if err := cmd.Wait(); err != nil && ctx.Err() == nil {
        slog.Warn("terminal-notifier exited with error", "err", err)
    }
    return result, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/notifier/... -v
```

Expected: all tests PASS. (The `Notify` function is not tested with a live subprocess — that requires `terminal-notifier` installed, which is an integration test.)

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: macOS terminal-notifier integration"
```

---

## Task 6: Telegram Template and Message Sending

**Files:**
- Modify: `internal/telegram/template.go`
- Create: `internal/telegram/template_test.go`
- Modify: `internal/telegram/bot.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/telegram/template_test.go`:

```go
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
        CreatedAt: time.Date(2026, 3, 31, 14, 30, 0, 0, time.UTC),
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
        CreatedAt: time.Now(),
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
        CreatedAt: time.Now(),
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
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/telegram/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement `internal/telegram/template.go`**

```go
package telegram

import (
    "bytes"
    "strings"
    "text/template"
    "time"
)

const maxToolInputLen = 3800

const defaultTemplate = `🔐 Claude Code Approval Required

Session: {{.SessionID}}
Tool:    {{.ToolName}}
Input:
` + "```" + `
{{.ToolInput}}
` + "```" + `

⏰ {{.CreatedAt}}

Waiting for response...`

// TemplateData holds the variables available in the Telegram message template.
type TemplateData struct {
    SessionID string
    ToolName  string
    ToolInput string // pre-truncated before rendering
    CreatedAt string // formatted time
}

// RenderMessage renders the Telegram notification message.
// If tmplStr is empty, the default template is used.
// toolInput is automatically truncated to maxToolInputLen before rendering.
func RenderMessage(tmplStr string, data TemplateData) (string, error) {
    if tmplStr == "" {
        tmplStr = defaultTemplate
    }
    tmpl, err := template.New("msg").Parse(tmplStr)
    if err != nil {
        return "", err
    }
    // Truncate ToolInput
    if len(data.ToolInput) > maxToolInputLen {
        data.ToolInput = data.ToolInput[:maxToolInputLen] + "...[truncated]"
    }
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", err
    }
    return strings.TrimSpace(buf.String()), nil
}

// FormatTemplateData builds TemplateData from raw request fields.
func FormatTemplateData(sessionID, toolName, toolInput string, createdAt time.Time) TemplateData {
    return TemplateData{
        SessionID: sessionID,
        ToolName:  toolName,
        ToolInput: toolInput,
        CreatedAt: createdAt.Format("15:04:05"),
    }
}
```

- [ ] **Step 4: Run template tests**

```bash
go test ./internal/telegram/... -run TestDefaultTemplate -v
go test ./internal/telegram/... -run TestCustomTemplate -v
go test ./internal/telegram/... -run TestInvalidTemplate -v
go test ./internal/telegram/... -run TestToolInputTruncation -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: Telegram message template rendering with truncation"
```

---

## Task 7: Telegram Bot (Long-Poll Loop and Sending)

**Files:**
- Modify: `internal/telegram/bot.go`
- Create: `internal/telegram/bot_test.go`

The bot struct wraps the telegram-bot-api client. It has two responsibilities: (1) send a message with inline keyboard for a given request, (2) run a long-poll loop that reads callbacks and routes them to the correct pending request via the store.

- [ ] **Step 1: Write the failing tests**

Create `internal/telegram/bot_test.go`:

```go
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
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/telegram/... -run TestCallback -v
```

Expected: compile error.

- [ ] **Step 3: Implement `internal/telegram/bot.go`**

```go
package telegram

import (
    "context"
    "fmt"
    "log/slog"
    "strings"
    "time"

    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    "github.com/vokomarov/claude-code-approvals/internal/approvals"
)

// CallbackData formats the callback_data string for a Telegram inline button.
func CallbackData(action, requestID string) string {
    return action + ":" + requestID
}

// ParseCallback parses a callback_data string into a decision value and request ID.
// Returns decision ("allow" or "deny") and the request UUID.
func ParseCallback(data string) (decision, requestID string, err error) {
    parts := strings.SplitN(data, ":", 2)
    if len(parts) != 2 {
        return "", "", fmt.Errorf("invalid callback data: %q", data)
    }
    switch parts[0] {
    case "approve":
        decision = "allow"
    case "deny":
        decision = "deny"
    default:
        return "", "", fmt.Errorf("unknown action in callback: %q", parts[0])
    }
    return decision, parts[1], nil
}

// Bot wraps the Telegram API client.
type Bot struct {
    api      *tgbotapi.BotAPI
    chatID   int64
    tmplStr  string // message template; empty = default
}

// NewBot creates a Bot. Returns an error if the token is invalid.
func NewBot(token string, chatID int64, messageTemplate string) (*Bot, error) {
    api, err := tgbotapi.NewBotAPI(token)
    if err != nil {
        return nil, fmt.Errorf("telegram bot init: %w", err)
    }
    return &Bot{api: api, chatID: chatID, tmplStr: messageTemplate}, nil
}

// SendApprovalRequest sends the approval notification for a request.
func (b *Bot) SendApprovalRequest(req *approvals.ApprovalRequest) error {
    data := FormatTemplateData(req.SessionID, req.ToolName, req.ToolInput, req.CreatedAt)
    text, err := RenderMessage(b.tmplStr, data)
    if err != nil {
        return fmt.Errorf("render template: %w", err)
    }

    keyboard := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("✅ Approve", CallbackData("approve", req.ID)),
            tgbotapi.NewInlineKeyboardButtonData("❌ Deny", CallbackData("deny", req.ID)),
        ),
    )
    msg := tgbotapi.NewMessage(b.chatID, text)
    msg.ParseMode = "Markdown"
    msg.ReplyMarkup = keyboard

    _, err = b.api.Send(msg)
    return err
}

// PollForever runs the Telegram long-poll loop until the context is cancelled.
// When an inline button is pressed, it looks up the request by UUID in the store
// and writes the decision to the request's Decision channel.
func (b *Bot) PollForever(ctx context.Context, store *approvals.Store) {
    u := tgbotapi.NewUpdate(0)
    u.Timeout = 30

    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        updates, err := b.api.GetUpdates(u)
        if err != nil {
            slog.Warn("telegram poll error, retrying", "err", err)
            select {
            case <-time.After(5 * time.Second):
            case <-ctx.Done():
                return
            }
            continue
        }

        for _, update := range updates {
            u.Offset = update.UpdateID + 1
            if update.CallbackQuery == nil {
                continue
            }
            b.handleCallback(update.CallbackQuery, store)
        }
    }
}

func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery, store *approvals.Store) {
    decision, requestID, err := ParseCallback(cb.Data)
    if err != nil {
        slog.Warn("invalid callback data", "data", cb.Data, "err", err)
        return
    }

    req, ok := store.Get(requestID)
    if !ok {
        slog.Warn("callback for unknown request", "id", requestID)
        return
    }

    // Non-blocking send: if already decided, silently drop.
    select {
    case req.Decision <- approvals.Decision{Value: decision, Source: "telegram"}:
        slog.Info("telegram decision received", "id", requestID, "decision", decision)
        req.Cancel()
    default:
        slog.Info("telegram callback ignored (already decided)", "id", requestID)
    }

    // Acknowledge the callback to remove the loading state in Telegram UI.
    ack := tgbotapi.NewCallback(cb.ID, "")
    _, _ = b.api.Request(ack)
}
```

- [ ] **Step 4: Run all telegram tests**

```bash
go test ./internal/telegram/... -v
```

Expected: all PASS (callback and template tests; bot tests that require real API tokens are not included).

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: Telegram bot long-polling, message sending, and callback routing"
```

---

## Task 8: Settings Manager (`cc on` / `cc off`)

**Files:**
- Modify: `internal/settings/settings.go`
- Create: `internal/settings/settings_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/settings/settings_test.go`:

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
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/settings/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement `internal/settings/settings.go`**

```go
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
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/settings/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: settings.json enable/disable with atomic write"
```

---

## Task 9: MCP Handler

**Files:**
- Modify: `internal/mcp/handler.go`
- Create: `internal/mcp/handler_test.go`

The MCP handler registers the `request_permission` tool on an MCP server. When called, it creates an `ApprovalRequest`, adds it to the store, starts the state machine, then blocks until the Decision channel receives a value.

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/handler_test.go`:

```go
package mcp_test

import (
    "testing"
    "time"

    "github.com/vokomarov/claude-code-approvals/internal/approvals"
    "github.com/vokomarov/claude-code-approvals/internal/mcp"
)

func TestHandlePermissionRequestAutoApprove(t *testing.T) {
    store := approvals.NewStore()
    opts := mcp.HandlerOpts{
        Store:           store,
        MacosSeconds:    0,
        TelegramSeconds: 0,
        TotalSeconds:    1,
        TimeoutPolicy:   "allow",
        OnMacos:         func(r *approvals.ApprovalRequest) {},
        OnTelegram:      func(r *approvals.ApprovalRequest) {},
    }

    start := time.Now()
    decision, err := mcp.HandlePermissionRequest(opts, "sess1", "Bash", `{"command":"ls"}`)
    elapsed := time.Since(start)

    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if decision != "allow" {
        t.Errorf("expected allow from timeout policy, got %q", decision)
    }
    if elapsed < time.Second || elapsed > 3*time.Second {
        t.Errorf("expected ~1s elapsed, got %v", elapsed)
    }
}

func TestHandlePermissionRequestCleanup(t *testing.T) {
    store := approvals.NewStore()
    opts := mcp.HandlerOpts{
        Store:           store,
        MacosSeconds:    0,
        TelegramSeconds: 0,
        TotalSeconds:    1,
        TimeoutPolicy:   "deny",
        OnMacos:         func(r *approvals.ApprovalRequest) {},
        OnTelegram:      func(r *approvals.ApprovalRequest) {},
    }

    _, _ = mcp.HandlePermissionRequest(opts, "s", "Bash", "{}")

    // After completion, store should be empty
    all := store.All()
    if len(all) != 0 {
        t.Errorf("expected store to be empty after completion, got %d requests", len(all))
    }
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/mcp/... -v -timeout 10s
```

Expected: compile error.

- [ ] **Step 3: Implement `internal/mcp/handler.go`**

```go
package mcp

import (
    "log/slog"

    "github.com/vokomarov/claude-code-approvals/internal/approvals"
)

// HandlerOpts configures the permission request handler.
type HandlerOpts struct {
    Store           *approvals.Store
    MacosSeconds    int
    TelegramSeconds int
    TotalSeconds    int
    TimeoutPolicy   string
    OnMacos         func(*approvals.ApprovalRequest)
    OnTelegram      func(*approvals.ApprovalRequest)
}

// HandlePermissionRequest creates a request, runs the state machine, blocks until decided,
// cleans up, and returns the decision value ("allow" or "deny").
func HandlePermissionRequest(opts HandlerOpts, sessionID, toolName, toolInput string) (string, error) {
    req := approvals.NewRequest(sessionID, toolName, toolInput)

    slog.Info("permission request received",
        "id", req.ID,
        "session", sessionID,
        "tool", toolName,
    )

    opts.Store.Add(req)
    defer func() {
        opts.Store.Delete(req.ID)
        req.Cancel()
    }()

    approvals.RunMachine(req, approvals.MachineOpts{
        MacosSeconds:    opts.MacosSeconds,
        TelegramSeconds: opts.TelegramSeconds,
        TotalSeconds:    opts.TotalSeconds,
        TimeoutPolicy:   opts.TimeoutPolicy,
        OnMacos:         opts.OnMacos,
        OnTelegram:      opts.OnTelegram,
    })

    // Block until decided.
    decision := <-req.Decision

    slog.Info("permission request decided",
        "id", req.ID,
        "decision", decision.Value,
        "source", decision.Source,
    )

    return decision.Value, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/mcp/... -v -timeout 15s
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: MCP permission request handler with state machine integration"
```

---

## Task 10: Daemon Server (Wire Everything)

**Files:**
- Modify: `internal/daemon/server.go`

No unit tests for the daemon wiring itself (it requires real network ports). This is covered by the integration smoke test in Task 12.

- [ ] **Step 1: Implement `internal/daemon/server.go`**

```go
package daemon

import (
    "context"
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "text/template"
    "time"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"
    "github.com/vokomarov/claude-code-approvals/internal/approvals"
    approvalmcp "github.com/vokomarov/claude-code-approvals/internal/mcp"
    "github.com/vokomarov/claude-code-approvals/internal/notifier"
    "github.com/vokomarov/claude-code-approvals/internal/telegram"
    "github.com/vokomarov/claude-code-approvals/internal/config"
)

// Server holds all daemon state.
type Server struct {
    cfg        *config.Config
    store      *approvals.Store
    bot        *telegram.Bot
    httpServer *http.Server
    mcpServer  *server.SSEServer
}

// New creates a Server. Returns an error if prerequisites fail
// (port conflict, invalid Telegram token, invalid message template).
func New(cfg *config.Config) (*Server, error) {
    // Validate message template at startup
    if cfg.Telegram.MessageTemplate != "" {
        if _, err := template.New("").Parse(cfg.Telegram.MessageTemplate); err != nil {
            return nil, fmt.Errorf("invalid message_template: %w", err)
        }
    }

    // Check terminal-notifier availability
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
    return s, nil
}

// Run starts the daemon and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
    addr := fmt.Sprintf(":%d", s.cfg.Daemon.Port)

    // Start Telegram long-poll loop
    if s.bot != nil {
        go s.bot.PollForever(ctx, s.store)
    }

    // Build MCP server
    mcpSrv := server.NewMCPServer("cc-approvals", "1.0.0")
    tool := mcp.NewTool("request_permission",
        mcp.WithDescription("Request user approval for a Claude Code action"),
        mcp.WithString("tool_name", mcp.Required(), mcp.Description("Name of the tool requesting permission")),
        mcp.WithObject("tool_input", mcp.Required(), mcp.Description("Tool input parameters")),
        mcp.WithString("session_id", mcp.Description("Claude Code session identifier")),
    )

    cfg := s.cfg
    store := s.store
    bot := s.bot
    handlerOpts := approvalmcp.HandlerOpts{
        Store:           store,
        MacosSeconds:    cfg.Timeouts.MacosNotificationSeconds,
        TelegramSeconds: cfg.Timeouts.TelegramNotificationSeconds,
        TotalSeconds:    cfg.Timeouts.TotalTimeoutSeconds,
        TimeoutPolicy:   cfg.Timeouts.TimeoutPolicy,
        OnMacos: func(req *approvals.ApprovalRequest) {
            if cfg.Timeouts.MacosNotificationSeconds == 0 {
                return
            }
            title := fmt.Sprintf("Claude Code – %s", req.ToolName)
            message := notifier.TruncateForMacOS(req.ToolInput)
            timeoutSecs := cfg.Timeouts.TelegramNotificationSeconds - cfg.Timeouts.MacosNotificationSeconds
            if timeoutSecs <= 0 {
                timeoutSecs = 30
            }
            go func() {
                result, err := notifier.Notify(ctx, title, message, cfg.MacOS.PhpStormBundleID, timeoutSecs)
                if err != nil {
                    slog.Warn("terminal-notifier error", "id", req.ID, "err", err)
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
                case req.Decision <- approvals.Decision{Value: decision, Source: "macos"}:
                    slog.Info("macOS decision received", "id", req.ID, "decision", decision)
                    req.Cancel()
                default:
                }
            }()
        },
        OnTelegram: func(req *approvals.ApprovalRequest) {
            if bot == nil {
                return
            }
            if err := bot.SendApprovalRequest(req); err != nil {
                slog.Error("telegram send failed", "id", req.ID, "err", err)
            }
        },
    }

    mcpSrv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        toolName, _ := req.Params.Arguments["tool_name"].(string)
        toolInputRaw, _ := req.Params.Arguments["tool_input"]
        sessionID, _ := req.Params.Arguments["session_id"].(string)

        toolInputJSON := fmt.Sprintf("%v", toolInputRaw)

        decision, err := approvalmcp.HandlePermissionRequest(handlerOpts, sessionID, toolName, toolInputJSON)
        if err != nil {
            return mcp.NewToolResultError(err.Error()), nil
        }
        return mcp.NewToolResultText(fmt.Sprintf(`{"decision":"%s"}`, decision)), nil
    })

    // HTTP mux: health + MCP SSE
    mux := http.NewServeMux()
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        fmt.Fprint(w, `{"status":"ok"}`)
    })

    sseSrv := server.NewSSEServer(mcpSrv, server.WithBaseURL(fmt.Sprintf("http://localhost:%d", cfg.Daemon.Port)))
    mux.Handle("/mcp", sseSrv)
    mux.Handle("/mcp/", sseSrv)

    s.httpServer = &http.Server{Addr: addr, Handler: mux}

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
    // Apply timeout_policy to all pending requests
    pending := s.store.All()
    for _, req := range pending {
        select {
        case req.Decision <- approvals.Decision{Value: s.cfg.Timeouts.TimeoutPolicy, Source: "timeout"}:
        default:
        }
        req.Cancel()
    }

    // Give in-flight responses 5s to flush
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := s.httpServer.Shutdown(ctx); err != nil {
        slog.Warn("http shutdown error", "err", err)
    }
    slog.Info("daemon stopped", "pending_flushed", len(pending))
}
```

- [ ] **Step 2: Build to verify compilation**

```bash
go build ./...
```

Expected: no errors. Fix any import issues (the mcp-go API may differ slightly — adjust to match the library's actual interface).

- [ ] **Step 3: Commit**

```bash
git add .
git commit -m "feat: daemon server wiring MCP, HTTP health, Telegram, and macOS notifier"
```

---

## Task 11: CLI Entry Point (`cc daemon`, `cc on`, `cc off`)

**Files:**
- Modify: `cmd/cc/main.go`
- Create: `launchd/com.vokomarov.cc-approvals.plist`

- [ ] **Step 1: Implement `cmd/cc/main.go`**

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
    case "on":
        runOn()
    case "off":
        runOff()
    default:
        fmt.Fprintf(os.Stderr, "unknown command: %q\n", os.Args[1])
        printUsage()
        os.Exit(1)
    }
}

func printUsage() {
    fmt.Fprintln(os.Stderr, "Usage: cc <command>")
    fmt.Fprintln(os.Stderr, "  daemon  Start the approval daemon (used by launchd)")
    fmt.Fprintln(os.Stderr, "  on      Enable notifications (modifies Claude Code settings)")
    fmt.Fprintln(os.Stderr, "  off     Disable notifications (modifies Claude Code settings)")
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

func runOn() {
    cfgPath := config.DefaultPath()
    cfg, err := config.Load(cfgPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
        os.Exit(1)
    }

    // Check daemon is running
    if !isDaemonHealthy(cfg.Daemon.Port) {
        fmt.Fprintf(os.Stderr, "warning: daemon does not appear to be running on port %d\n", cfg.Daemon.Port)
        fmt.Fprintf(os.Stderr, "         start it with: launchctl start com.vokomarov.cc-approvals\n")
    }

    settingsPath := cfg.Paths.ClaudeSettings
    if err := settings.Enable(settingsPath, cfg.Daemon.Port); err != nil {
        fmt.Fprintf(os.Stderr, "error updating settings: %v\n", err)
        os.Exit(1)
    }

    fmt.Println("✅ Notifications enabled.")
    fmt.Printf("   Settings updated: %s\n", settingsPath)
    fmt.Println("   Restart your Claude Code session in PHPStorm to apply.")
}

func runOff() {
    cfgPath := config.DefaultPath()
    cfg, err := config.Load(cfgPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
        os.Exit(1)
    }

    settingsPath := cfg.Paths.ClaudeSettings
    if err := settings.Disable(settingsPath); err != nil {
        fmt.Fprintf(os.Stderr, "error updating settings: %v\n", err)
        os.Exit(1)
    }

    fmt.Println("✅ Notifications disabled.")
    fmt.Printf("   Settings updated: %s\n", settingsPath)
    fmt.Println("   Restart your Claude Code session in PHPStorm to apply.")
}

func isDaemonHealthy(port int) bool {
    client := &http.Client{Timeout: 2 * time.Second}
    resp, err := client.Get(fmt.Sprintf("http://localhost:%d/health", port))
    if err != nil {
        return false
    }
    defer resp.Body.Close()
    return resp.StatusCode == http.StatusOK
}
```

- [ ] **Step 2: Create `launchd/com.vokomarov.cc-approvals.plist`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.vokomarov.cc-approvals</string>

    <key>ProgramArguments</key>
    <array>
        <string>/Users/vokomarov/go/bin/cc</string>
        <string>daemon</string>
    </array>

    <key>KeepAlive</key>
    <true/>

    <key>ThrottleInterval</key>
    <integer>10</integer>

    <key>RunAtLoad</key>
    <true/>

    <key>StandardOutPath</key>
    <string>/tmp/cc-approvals.log</string>

    <key>StandardErrorPath</key>
    <string>/tmp/cc-approvals-error.log</string>
</dict>
</plist>
```

- [ ] **Step 3: Build the final binary**

```bash
go build ./cmd/cc
./cc
```

Expected: prints usage message with `daemon`, `on`, `off` commands listed.

- [ ] **Step 4: Run all tests one final time**

```bash
go test ./... -v -timeout 30s
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: unified cc binary with daemon/on/off subcommands and launchd plist"
```

---

## Task 12: Install and Smoke Test

**Files:**
- No new files — this task validates the full system end-to-end.

- [ ] **Step 1: Install dependencies and binary**

```bash
brew install terminal-notifier
go install ./cmd/cc
```

Expected: `cc` binary at `~/go/bin/cc`. Confirm: `which cc` returns `~/go/bin/cc`.

- [ ] **Step 2: Set up config**

```bash
mkdir -p ~/.config/cc-approvals
cp config.example.yaml ~/.config/cc-approvals/config.yaml
# Edit the file: set your real bot_token and chat_id
```

- [ ] **Step 3: Validate config loads**

```bash
cc daemon &
sleep 1
curl -s http://localhost:9753/health
kill %1
```

Expected: `{"status":"ok"}` returned from health endpoint.

- [ ] **Step 4: Test `cc on`**

```bash
cc on
cat ~/.dotfiles/config/claude/settings.json | grep permissionPromptTool
```

Expected: `"permissionPromptTool": "mcp__cc-approvals__request_permission"` present.

- [ ] **Step 5: Test `cc off`**

```bash
cc off
cat ~/.dotfiles/config/claude/settings.json | grep -c permissionPromptTool
```

Expected: output is `0` (key removed).

- [ ] **Step 6: Install launchd service**

```bash
cp launchd/com.vokomarov.cc-approvals.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.vokomarov.cc-approvals.plist
sleep 2
curl -s http://localhost:9753/health
```

Expected: `{"status":"ok"}`.

- [ ] **Step 7: End-to-end notification test**

```bash
cc on
# Open PHPStorm and start a new Claude Code session
# Ask Claude Code to run a bash command
# Observe: after 15s, macOS notification should appear with Approve/Deny
# Click Approve — Claude Code should proceed
```

- [ ] **Step 8: Final commit**

```bash
git add .
git commit -m "chore: add config.example.yaml and verify end-to-end installation"
```

---

## Summary

| Task | What it builds |
|---|---|
| 1 | Project scaffold, go.mod, stub files |
| 2 | Config loading and validation |
| 3 | ApprovalRequest types and thread-safe store |
| 4 | State machine with two configurable timers |
| 5 | macOS notifier (terminal-notifier subprocess) |
| 6 | Telegram template rendering and truncation |
| 7 | Telegram bot long-poll loop and callback routing |
| 8 | Settings.json enable/disable with atomic write |
| 9 | MCP permission request handler |
| 10 | Daemon server wiring all components |
| 11 | CLI entry point (`cc daemon`/`on`/`off`) + launchd plist |
| 12 | Installation and end-to-end smoke test |
