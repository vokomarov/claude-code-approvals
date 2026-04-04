# Infinite Total Timeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow `total_timeout_seconds: 0` in config to mean "wait indefinitely," making `timeout_policy` optional in that case and ensuring shutdown always denies pending requests.

**Architecture:** Three surgical changes: (1) loosen config validation to accept `total == 0` and skip `timeout_policy` check when total is zero; (2) skip the total-timeout goroutine in `RunMachine` when `TotalSeconds == 0`; (3) hardcode "deny" in the shutdown path. All existing behaviour when `total > 0` is preserved unchanged.

**Tech Stack:** Go 1.22+, standard library only (`context`, `time`). Test runner: `go test ./...`.

---

## Files

| Action | File | Change |
|--------|------|--------|
| Modify | `internal/config/config.go` | Loosen `validate`: allow `total == 0`, skip `timeout_policy` check when total is zero |
| Modify | `internal/approvals/machine.go` | Skip total-timeout goroutine when `TotalSeconds == 0`; update comments |
| Modify | `internal/daemon/server.go` | Hardcode `"deny"` in `shutdown()` |
| Modify | `config.example.yaml` | Document zero semantics for `total_timeout_seconds` and `timeout_policy` |
| Modify | `CLAUDE.md` | Update config constraints description |

---

### Task 1: Config validation — allow `total_timeout_seconds: 0`

**Files:**
- Modify: `internal/config/config.go` (function `validate`, lines 77–110)

The current `validate` function rejects `total <= 0`. We need to:
- Allow `total == 0` (infinite wait)
- Only validate `timeout_policy` and the `total > maxNotification` rule when `total > 0`

- [ ] **Step 1: Write failing tests**

Add these cases to `internal/config/config_test.go`. If the file doesn't exist yet, create it with the package declaration and imports first.

```go
package config_test

import (
	"testing"
)

func validBase() Config {
	return Config{
		Telegram: Telegram{BotToken: "tok", ChatID: 1},
		Timeouts: Timeouts{
			MacosNotificationSeconds:    0,
			TelegramNotificationSeconds: 0,
			TotalTimeoutSeconds:         60,
			TimeoutPolicy:               "deny",
		},
		Daemon: Daemon{Port: 9753},
		Paths:  Paths{ClaudeSettings: "/tmp/settings.json"},
	}
}

func TestValidateTotalZeroIsValid(t *testing.T) {
	cfg := validBase()
	cfg.Timeouts.TotalTimeoutSeconds = 0
	cfg.Timeouts.TimeoutPolicy = "" // not required when total == 0
	if err := validate(&cfg); err != nil {
		t.Errorf("expected no error for total=0, got: %v", err)
	}
}

func TestValidateTotalZeroWithNotificationsIsValid(t *testing.T) {
	cfg := validBase()
	cfg.Timeouts.TotalTimeoutSeconds = 0
	cfg.Timeouts.TimeoutPolicy = ""
	cfg.Timeouts.MacosNotificationSeconds = 15
	cfg.Timeouts.TelegramNotificationSeconds = 30
	if err := validate(&cfg); err != nil {
		t.Errorf("expected no error for total=0 with notifications, got: %v", err)
	}
}

func TestValidateTotalPositiveStillRequiresPolicy(t *testing.T) {
	cfg := validBase()
	cfg.Timeouts.TimeoutPolicy = ""
	if err := validate(&cfg); err == nil {
		t.Error("expected error when total>0 and timeout_policy is empty")
	}
}

func TestValidateTotalPositiveStillRequiresExceedNotification(t *testing.T) {
	cfg := validBase()
	cfg.Timeouts.MacosNotificationSeconds = 15
	cfg.Timeouts.TelegramNotificationSeconds = 30
	cfg.Timeouts.TotalTimeoutSeconds = 20 // less than max notification (30)
	if err := validate(&cfg); err == nil {
		t.Error("expected error when total <= max notification timeout")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/config/... -run "TestValidateTotal" -v
```

Expected: `TestValidateTotalZeroIsValid` and `TestValidateTotalZeroWithNotificationsIsValid` FAIL (current validation rejects total=0). The other two should PASS.

- [ ] **Step 3: Update `validate` in `internal/config/config.go`**

Replace the timeout validation block (lines 84–102) with:

```go
if cfg.Timeouts.TotalTimeoutSeconds < 0 {
	return fmt.Errorf("total_timeout_seconds must be >= 0")
}
if cfg.Timeouts.TotalTimeoutSeconds > 0 {
	if cfg.Timeouts.TimeoutPolicy != "deny" && cfg.Timeouts.TimeoutPolicy != "approve" {
		return fmt.Errorf("timeouts.timeout_policy must be 'deny' or 'approve', got %q", cfg.Timeouts.TimeoutPolicy)
	}
	m := cfg.Timeouts.MacosNotificationSeconds
	tg := cfg.Timeouts.TelegramNotificationSeconds
	total := cfg.Timeouts.TotalTimeoutSeconds
	maxNotification := m
	if tg > maxNotification {
		maxNotification = tg
	}
	if maxNotification > 0 && total <= maxNotification {
		return fmt.Errorf("total_timeout_seconds (%d) must be greater than the largest notification timeout (%d)", total, maxNotification)
	}
}
```

Also remove the old standalone `timeout_policy` check (lines 84–86) and the old `total <= 0` check (line 100) — they are now subsumed by the block above. The `m < 0 || tg < 0` check can remain as-is above this block.

The full updated `validate` should read:

```go
func validate(cfg *Config) error {
	if cfg.Telegram.BotToken == "" {
		return fmt.Errorf("telegram.bot_token is required")
	}
	if cfg.Telegram.ChatID == 0 {
		return fmt.Errorf("telegram.chat_id is required")
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
	if total < 0 {
		return fmt.Errorf("total_timeout_seconds must be >= 0")
	}
	if total > 0 {
		if cfg.Timeouts.TimeoutPolicy != "deny" && cfg.Timeouts.TimeoutPolicy != "approve" {
			return fmt.Errorf("timeouts.timeout_policy must be 'deny' or 'approve', got %q", cfg.Timeouts.TimeoutPolicy)
		}
		maxNotification := m
		if tg > maxNotification {
			maxNotification = tg
		}
		if maxNotification > 0 && total <= maxNotification {
			return fmt.Errorf("total_timeout_seconds (%d) must be greater than the largest notification timeout (%d)", total, maxNotification)
		}
	}
	if cfg.Daemon.Port < 1 || cfg.Daemon.Port > 65535 {
		return fmt.Errorf("daemon.port must be between 1 and 65535")
	}
	if cfg.Paths.ClaudeSettings == "" {
		return fmt.Errorf("paths.claude_settings is required")
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/config/... -v
```

Expected: all tests PASS including the four new ones.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): allow total_timeout_seconds=0 for infinite wait"
```

---

### Task 2: Machine — skip total-timeout goroutine when `TotalSeconds == 0`

**Files:**
- Modify: `internal/approvals/machine.go`
- Modify: `internal/approvals/machine_test.go`

- [ ] **Step 1: Write a failing test**

Add to `internal/approvals/machine_test.go`:

```go
func TestMachineNoTotalTimeoutWaitsIndefinitely(t *testing.T) {
	req := approvals.NewRequest("s", "Bash", "{}")

	approvals.RunMachine(req, approvals.MachineOpts{
		MacosSeconds:    0,
		TelegramSeconds: 0,
		TotalSeconds:    0, // infinite — no goroutine should fire a decision
		TimeoutPolicy:   "",
		OnMacos:         func(r *approvals.ApprovalRequest) {},
		OnTelegram:      func(r *approvals.ApprovalRequest) {},
	})

	// After 200ms, no decision should have been produced automatically.
	select {
	case d := <-req.Decision:
		t.Errorf("expected no decision, got %+v", d)
	case <-time.After(200 * time.Millisecond):
		// success: machine is waiting indefinitely
	}
	req.Cancel()
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/approvals/... -run "TestMachineNoTotalTimeout" -v
```

Expected: FAIL — the current code starts a timer with `time.Duration(0)` which fires immediately, producing an unintended decision.

- [ ] **Step 3: Update `RunMachine` in `internal/approvals/machine.go`**

Wrap the total-timeout goroutine in a `if opts.TotalSeconds > 0` guard, and update the comments:

```go
// MachineOpts configures the state machine for a single request.
//
// OnMacos and OnTelegram are notification callbacks triggered at their configured
// times. They are responsible for sending the external notification (e.g. spawning
// terminal-notifier or sending a Telegram message) and writing the result to
// req.Decision. They must NOT call req.Cancel() themselves — context cleanup is
// the consumer's responsibility (typically the daemon handler after reading Decision).
type MachineOpts struct {
	MacosSeconds    int    // 0 = skip macOS notification
	TelegramSeconds int    // 0 = skip Telegram notification
	TotalSeconds    int    // 0 = no hard ceiling (wait indefinitely); >0 = hard ceiling in seconds
	TimeoutPolicy   string // "allow" | "deny" — only consulted when TotalSeconds > 0
	OnMacos         func(*ApprovalRequest)
	OnTelegram      func(*ApprovalRequest)
}
```

And in `RunMachine`, replace the unconditional total-timeout goroutine block with:

```go
// Total timeout: only runs when TotalSeconds > 0. When zero, the request
// waits indefinitely until a notification callback writes to Decision or
// the daemon shuts down.
if opts.TotalSeconds > 0 {
	go func() {
		t := time.NewTimer(time.Duration(opts.TotalSeconds) * time.Second)
		defer t.Stop()
		select {
		case <-t.C:
			// Non-blocking send: if already decided, this is a no-op.
			select {
			case req.Decision <- Decision{Value: opts.TimeoutPolicy, Source: "timeout"}:
			default:
			}
			cancel()
		case <-ctx.Done():
		}
	}()
}
```

- [ ] **Step 4: Run all machine tests**

```bash
go test ./internal/approvals/... -v
```

Expected: all tests PASS, including the new `TestMachineNoTotalTimeoutWaitsIndefinitely`.

- [ ] **Step 5: Commit**

```bash
git add internal/approvals/machine.go internal/approvals/machine_test.go
git commit -m "feat(machine): skip total-timeout goroutine when TotalSeconds=0"
```

---

### Task 3: Shutdown — always deny pending requests

**Files:**
- Modify: `internal/daemon/server.go` (function `shutdown`, lines 200–216)
- Modify: `internal/daemon/server_http_test.go` (add/update shutdown test if present)

- [ ] **Step 1: Check existing shutdown test coverage**

Read `internal/daemon/server_http_test.go` and check whether `shutdown()` is tested directly. If there is a test that verifies the shutdown sends `TimeoutPolicy`, it will need updating.

```bash
grep -n "shutdown\|TimeoutPolicy" internal/daemon/server_http_test.go
```

- [ ] **Step 2: Write a failing test (if shutdown is not yet tested)**

If no shutdown test exists, add one. If one exists that asserts `TimeoutPolicy`, update it to assert `"deny"`.

```go
func TestShutdownDeniesAllPendingRequests(t *testing.T) {
	cfg := &config.Config{
		Telegram: config.Telegram{BotToken: "tok", ChatID: 1},
		Timeouts: config.Timeouts{
			TotalTimeoutSeconds: 0,   // infinite — shutdown must still deny
			TimeoutPolicy:       "",  // not set when total=0
		},
		Daemon: config.Daemon{Port: 0},
		Paths:  config.Paths{ClaudeSettings: "/tmp/s.json"},
	}
	s := &daemon.ServerForTest(cfg) // see note below

	req := approvals.NewRequest("sess", "Bash", "{}")
	s.Store().Add(req)

	s.Shutdown()

	select {
	case d := <-req.Decision:
		if d.Value != "deny" {
			t.Errorf("expected deny on shutdown, got %q", d.Value)
		}
		if d.Source != "timeout" {
			t.Errorf("expected source=timeout, got %q", d.Source)
		}
	case <-time.After(time.Second):
		t.Error("shutdown did not decide pending request")
	}
}
```

> **Note:** If `Server` and `shutdown` are unexported, test via the existing HTTP-level test pattern in `server_http_test.go` — cancel the server context and observe the hook response. Adapt the test structure to match whatever is already present in that file.

- [ ] **Step 3: Update `shutdown` in `internal/daemon/server.go`**

Change the decision sent during shutdown from `s.cfg.Timeouts.TimeoutPolicy` to hardcoded `"deny"`:

```go
func (s *Server) shutdown() {
	pending := s.store.All()
	for _, req := range pending {
		select {
		case req.Decision <- approvals.Decision{Value: "deny", Source: "timeout"}:
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

- [ ] **Step 4: Run all daemon tests**

```bash
go test ./internal/daemon/... -v
```

Expected: all tests PASS.

- [ ] **Step 5: Run full test suite with race detector**

```bash
go test -race ./...
```

Expected: all tests PASS, no race conditions.

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/server.go internal/daemon/server_http_test.go
git commit -m "fix(daemon): always deny pending requests on shutdown"
```

---

### Task 4: Update `config.example.yaml` and `CLAUDE.md`

**Files:**
- Modify: `config.example.yaml`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update `config.example.yaml`**

Replace the `timeouts` block with:

```yaml
timeouts:
  macos_notification_seconds: 15   # delay before macOS notification fires; 0 = skip macOS notification entirely
  telegram_notification_seconds: 30 # delay before Telegram notification fires; 0 = skip Telegram notification entirely
  total_timeout_seconds: 300        # hard ceiling in seconds; 0 = wait indefinitely (no timeout)
  timeout_policy: deny              # deny | approve — only used when total_timeout_seconds > 0
```

- [ ] **Step 2: Update `CLAUDE.md` config constraints**

Find the config validation description in `CLAUDE.md` (under "Configuration" or "Architecture"). Update the bullet about `total_timeout_seconds`:

Replace:
```
- `total_timeout_seconds` must exceed the largest notification timeout
- `timeout_policy` must be `deny` or `approve`
```

With:
```
- `total_timeout_seconds: 0` means wait indefinitely; any positive value is a hard ceiling and must exceed the largest notification timeout
- `timeout_policy` must be `deny` or `approve` when `total_timeout_seconds > 0`; ignored when total is zero
- On daemon shutdown, all pending requests are always denied regardless of `timeout_policy`
```

- [ ] **Step 3: Commit**

```bash
git add config.example.yaml CLAUDE.md
git commit -m "docs: update config docs for infinite total timeout"
```

---

## Self-Review

**Spec coverage:**
- ✅ `total_timeout_seconds: 0` accepted by validation → Task 1
- ✅ `timeout_policy` not required when total = 0 → Task 1
- ✅ `total > maxNotification` rule only when total > 0 → Task 1
- ✅ `RunMachine` skips goroutine when `TotalSeconds == 0` → Task 2
- ✅ `shutdown` always sends "deny" → Task 3
- ✅ `config.example.yaml` comments updated → Task 4
- ✅ `CLAUDE.md` updated → Task 4

**Placeholder scan:** None found.

**Type consistency:** `approvals.Decision{Value: "deny", Source: "timeout"}` used consistently in Task 3 shutdown, matching existing usage in `machine.go` and `server.go`. `MachineOpts` struct fields unchanged in name.
