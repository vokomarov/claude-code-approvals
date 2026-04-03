# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

`cc-approvals` is a macOS daemon that intercepts Claude Code permission prompts and routes them to the user via macOS native notifications (`terminal-notifier`) and/or Telegram inline-button messages. It exposes an MCP (Model Context Protocol) server over HTTP SSE that Claude Code connects to as a `permissionPromptTool`.

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
claude-code-approvals on        # inject permissionPromptTool into Claude Code settings.json
claude-code-approvals off       # remove permissionPromptTool from Claude Code settings.json
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
Claude Code
    │
    │  POST /mcp  (SSE)
    ▼
daemon/Server  ──────────────────────────────────────────────────────────┐
    │  mcp.HandlePermissionRequest(sessionID, toolName, toolInput)       │
    ▼                                                                    │
approvals.Store.Add(req)                                                 │
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
{"decision":"allow"} or {"decision":"deny"}  →  Claude Code proceeds/blocks
```

**Shutdown path** (`cc daemon` receives SIGTERM/SIGINT):

```
signal → ctx.Done()
    └─ Server.shutdown()
            ├─ store.All() → send TimeoutPolicy to each req.Decision
            └─ httpServer.Shutdown(5s) → flush in-flight SSE responses
```

### Package responsibilities

| Package | Responsibility |
|---|---|
| `cmd/cc` | CLI entry point; `daemon`, `on`, `off` sub-commands |
| `internal/daemon` | Wires config → store → bot → MCP server → HTTP mux; manages graceful shutdown |
| `internal/approvals` | `ApprovalRequest` type, `Store` (in-memory, mutex-guarded), `RunMachine` state machine |
| `internal/mcp` | `HandlePermissionRequest` — the blocking bridge between MCP tool call and the state machine |
| `internal/config` | YAML config load + validation; default path `~/.config/cc-approvals/config.yaml` |
| `internal/settings` | Read/write Claude Code `settings.json`; injects/removes `permissionPromptTool` key |
| `internal/notifier` | Wraps `terminal-notifier` CLI; `Notify` blocks until user clicks or timeout |
| `internal/telegram` | Long-poll bot loop; sends approval messages and handles inline-button callbacks |

### Concurrency model

- `req.Decision` is a **buffered channel of size 1**. The first write wins; all others are no-ops via `select { … default: }`.
- `req.Cancel` is a `context.CancelFunc` that stops all machine goroutines. It is called by the MCP handler (not by notification callbacks).
- Telegram bot runs `PollForever` in a single goroutine; it writes decisions to `req.Decision` by looking up requests in `Store`.
- Graceful shutdown (`Server.shutdown`) iterates all pending requests and sends the configured `timeout_policy` decision before calling `httpServer.Shutdown`.

### Configuration

Config lives at `~/.config/cc-approvals/config.yaml`. Key validated constraints:
- `telegram_notification_seconds` must exceed `macos_notification_seconds` by ≥ 5
- `total_timeout_seconds` must exceed the largest notification timeout
- `timeout_policy` must be `deny` or `approve`

Set either notification timeout to `0` to skip that channel entirely.

### MCP integration

The daemon registers as an MCP server at `http://localhost:<port>/mcp`. Claude Code's `settings.json` must contain:

```json
"permissionPromptTool": "mcp__cc-approvals__request_permission"
```

`cc on` / `cc off` inject or remove this key automatically.

## Environment notes

- Config lives at `./config.yml` (gitignored) — copy from `config.example.yaml` and fill in Telegram credentials before starting the daemon
- `~/go/bin` must be in `PATH`; macOS ships `/usr/bin/cc` (clang) which shadows Go binaries
- `mcp-go`: use `req.GetArguments()` to access tool arguments — `req.Params.Arguments` is typed `any` in v0.45.0+

## Known Gaps (spec vs. implementation)

- `ApprovalRequest.ProjectPath` — field defined in `types.go`, never populated; spec says it should be the daemon CWD
- `telegram.bot_token` and `chat_id` are required by config validation even when `telegram_notification_seconds = 0` — intentional per spec
- `docs/plans/` is historical (all tasks complete); `docs/specs/` is the authoritative design reference
