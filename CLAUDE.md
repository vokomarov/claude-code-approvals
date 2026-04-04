# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

`cc-approvals` is a macOS daemon that intercepts Claude Code permission prompts and routes them to the user via macOS native notifications (`terminal-notifier`) and/or Telegram inline-button messages. Claude Code invokes the `hook` subcommand as a subprocess per permission request; the hook POSTs to the daemon's HTTP API and returns the decision as `hookSpecificOutput` JSON.

## Commands

```bash
# Build and install the binary
go install ./cmd/claude-code-approvals

# Run all tests
go test ./...

# Run tests with race detector (required before committing)
go test -race ./...

# Run a single package's tests
go test ./internal/approvals/...

# Lint
golangci-lint run

# Vet
go vet ./...
```

### CLI commands (after `go install ./cmd/claude-code-approvals`)

```bash
claude-code-approvals daemon    # start the approval daemon (normally run by launchd)
claude-code-approvals install   # one-time: register PermissionRequest hook in settings.json
claude-code-approvals uninstall # remove hook from settings.json
claude-code-approvals on        # enable approval intercepting (daemon must be running)
claude-code-approvals off       # disable approval intercepting (daemon stays running)
claude-code-approvals hook      # run as PermissionRequest hook subprocess (invoked by Claude Code)
```

### launchd service

```bash
launchctl load ~/Library/LaunchAgents/com.vokomarov.cc-approvals.plist
launchctl start com.vokomarov.cc-approvals
curl -s http://localhost:9753/health   # verify
```

## Architecture

### Request lifecycle

```
Claude Code  (per permission request)
    │
    │  spawns: claude-code-approvals hook
    ▼
hook process
    │  reads stdin JSON (tool_name, tool_input, session_id, cwd)
    │  POST /api/permission  (blocking HTTP)
    ▼
daemon/Server  ──────────────────────────────────────────────────────────┐
    │  checks enabled flag (atomic.Bool)                                 │
    │  disabled → 204 No Content → hook exits silently → Claude Code     │
    │            uses built-in interactive prompt                        │
    │                                                                    │
    │  enabled → approvals.Store.Add(req)                                │
    │                                                                    │
    ├─ approvals.RunMachine(req, opts)  ──────────────────────────────┐  │
    │       │                                                         │  │
    │       ├── goroutine: macos timer  (MacosSeconds)                │  │
    │       │       └─ OnMacos(req) → notifier.Notify() [blocking]    │  │
    │       │               └─ user clicks → req.Decision ← "allow"/"deny"
    │       │                                                         │  │
    │       ├── goroutine: telegram timer  (TelegramSeconds)          │  │
    │       │       └─ OnTelegram(req) → bot.SendApprovalRequest()    │  │
    │       │               └─ bot.PollForever() callback             │  │
    │       │                       └─ req.Decision ← "allow"/"deny"  │  │
    │       │                                                         │  │
    │       └── goroutine: total timeout  (TotalSeconds)              │  │
    │               └─ req.Decision ← TimeoutPolicy ("deny"/"approve")│  │
    │                                                                 │  │
    │  ◄─────── first write to req.Decision wins (cap-1 channel) ─────┘  │
    │                                                                    │
    ▼                                                                    │
decision := <-req.Decision                                               │
req.Cancel()  ← stops all machine goroutines                             │
store.Delete(req.ID)                                                     │
    │                                                                    │
    └────────────────────────────────────────────────────────────────────┘
    │
    ▼
hook writes hookSpecificOutput JSON to stdout → Claude Code proceeds/blocks
```

**Shutdown path** (`cc daemon` receives SIGTERM/SIGINT):

```
signal → ctx.Done()
    └─ Server.shutdown()
            ├─ store.All() → always send "deny" to each req.Decision
            └─ httpServer.Shutdown(5s) → flush in-flight responses
```

### Package responsibilities

| Package | Responsibility |
|---|---|
| `cmd/claude-code-approvals` | CLI entry point; `daemon`, `install`, `uninstall`, `on`, `off`, `hook` sub-commands |
| `internal/daemon` | HTTP server with `atomic.Bool` enabled flag; `/api/permission` (blocking), `/api/enable`, `/api/disable`; graceful shutdown |
| `internal/hook` | Hook subprocess: reads stdin JSON, POSTs to daemon, writes `hookSpecificOutput` to stdout on allow/deny; silent on 204 or error |
| `internal/approvals` | `ApprovalRequest` type, `Store` (in-memory, mutex-guarded), `RunMachine` state machine |
| `internal/config` | YAML config load + validation; default path `~/.config/cc-approvals/config.yaml` |
| `internal/settings` | `Install(path, binaryPath)` writes `hooks.PermissionRequest` entry; `Uninstall(path)` removes it; atomic write via temp file + rename |
| `internal/notifier` | Wraps `terminal-notifier` CLI; `Notify` blocks until user clicks or timeout |
| `internal/telegram` | Long-poll bot loop; sends approval messages and handles inline-button callbacks |

### Concurrency model

- `req.Decision` is a **buffered channel of size 1**. The first write wins; all others are no-ops via `select { … default: }`.
- `req.Cancel` is a `context.CancelFunc` that stops all machine goroutines. Called by the `/api/permission` HTTP handler after decision, not by notification callbacks.
- Telegram bot runs `PollForever` in a single goroutine; it writes decisions to `req.Decision` by looking up requests in `Store`.
- Graceful shutdown (`Server.shutdown`) iterates all pending requests and always sends "deny" (regardless of `timeout_policy`) before calling `httpServer.Shutdown`.

### Configuration

Config lives at `~/.config/cc-approvals/config.yaml`. Key validated constraints:
- `telegram_notification_seconds` must exceed `macos_notification_seconds` by ≥ 5
- `total_timeout_seconds: 0` means wait indefinitely; any positive value is a hard ceiling and must exceed the largest notification timeout
- `timeout_policy` must be `deny` or `approve` when `total_timeout_seconds > 0`; ignored when total is zero
- On daemon shutdown, all pending requests are always denied regardless of `timeout_policy`

Set either notification timeout to `0` to skip that channel entirely.

### Hook integration

`cc install` writes a `PermissionRequest` hook entry to Claude Code's `settings.json` (one-time setup). Claude Code then spawns `claude-code-approvals hook` for every permission request. `cc on`/`cc off` toggle the daemon's `atomic.Bool` enabled flag via HTTP without touching `settings.json` or requiring a session restart.

When disabled, the hook receives 204 and exits silently — Claude Code falls back to its built-in interactive prompt.

## Environment notes

- Config lives at `~/.config/cc-approvals/config.yaml` (gitignored) — copy from `config.example.yaml` and fill in Telegram credentials before starting the daemon
- `~/go/bin` must be in `PATH`; macOS ships `/usr/bin/cc` (clang) which shadows Go binaries
- `permissionPromptTool` settings.json key is undocumented and **does not work** in Claude Code IDE sessions — use the `PermissionRequest` hook instead

## Known Gaps

- `ApprovalRequest.ProjectPath` — field defined in `types.go`, never populated
- `telegram.bot_token` and `chat_id` required by config validation even when `telegram_notification_seconds = 0` — intentional
- `OnMacos` goroutine outlives its `ApprovalRequest` — the `notifier.Notify` subprocess runs until the macOS notification times out even after the request is decided; harmless due to `select { default: }` guard
- Design spec: `docs/superpowers/specs/2026-04-03-permission-hook-redesign.md`
