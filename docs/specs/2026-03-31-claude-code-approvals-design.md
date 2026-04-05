# Claude Code Mobile Approvals — Design Spec
**Date:** 2026-03-31
**Author:** Vovan (vokomarov)
**Status:** Approved for implementation planning

---

## Problem Statement

When Claude Code (PHPStorm IDE plugin) requests permission for a potentially destructive action, the approval prompt only appears in the PHPStorm terminal. If the developer is away from their laptop or focused in another window, the session stalls silently. This spec describes a system that escalates unanswered permission requests to macOS notifications and then to a Telegram bot, enabling remote approval or denial from an iOS device.

---

## Goals

- Intercept Claude Code permission requests via the `PermissionRequest` MCP mechanism
- Escalate unanswered requests in two configurable stages: macOS notification (default t=15s), then Telegram (default t=30s); either stage can be disabled by setting its timeout to zero
- Allow the developer to approve or deny from either channel
- First response across all channels wins; subsequent responses are discarded
- Enable/disable the system with a single command (`cc on` / `cc off`) that modifies Claude Code's settings file, taking effect on the next session
- Support multiple concurrent Claude Code sessions and multiple simultaneous pending approvals
- Gracefully degrade on any failure (daemon down, Telegram unreachable, terminal-notifier missing) — never permanently block a Claude Code session

## Non-Goals

- Restoring the interactive console Y/N prompt while notifications are active (architectural constraint of `permissionPromptTool`)
- `--dangerously-skip-permissions` bypass protection (accepted: that flag skips the MCP call entirely)
- Cloud hosting or remote daemon (local Mac only)
- iOS push notifications outside of Telegram
- Concurrent session limits or per-session rate limiting (personal single-developer tool)

---

## Architecture Overview

```
Claude Code (PHPStorm plugin)
    │
    │  MCP over SSE (when permissionPromptTool is set)
    ▼
cc-daemon  (Go, launchd service, localhost:9753)
    │
    ├── MCP SSE server  ← handles request_permission tool calls
    ├── HTTP server     ← health check endpoint
    ├── Approvals store ← concurrent map of pending requests
    │
    ├── t=15s ──► terminal-notifier subprocess (skipped if macos_notification_seconds=0)
    │               Approve / Deny buttons
    │               Body click → focus PHPStorm
    │               Auto-dismisses after (telegram_timeout - macos_timeout) seconds
    │
    └── t=30s ──► Telegram Bot API (skipped if telegram_notification_seconds=0)
                    Inline keyboard: ✅ Approve / ❌ Deny
                    Configurable message template
```

### Enable / Disable

```
cc on   →  writes permissionPromptTool into ~/.dotfiles/config/claude/settings.json
cc off  →  removes permissionPromptTool from ~/.dotfiles/config/claude/settings.json
```

Effect applies on next Claude Code session start. The daemon itself always runs via launchd regardless of on/off state.

---

## Two-Timer State Machine

Each permission request is an independent state machine driven by three competing goroutines.

```
t=0    Request arrives (tool_name + tool_input + session_id)
       State: PENDING

t=15s  No response yet AND macos_notification_seconds > 0
       → Spawn terminal-notifier subprocess with Approve/Deny buttons
       → Notification auto-dismisses after (telegram_seconds - macos_seconds) seconds
       State: NOTIFIED_MACOS
       (if macos_notification_seconds = 0, this phase is skipped entirely)

t=30s  Still no response (macOS notification not acted on) AND telegram_notification_seconds > 0
       → Send Telegram message with inline keyboard
       State: NOTIFIED_BOTH
       (terminal-notifier subprocess still running until its own timeout — either can win)
       (if telegram_notification_seconds = 0, this phase is skipped entirely)

If both timeouts are 0: request proceeds directly to total_timeout_seconds → timeout_policy applied.

First response wins:
  macOS Approve/Deny button clicked  → DECIDED(allow|deny), cancel Telegram goroutine
  Telegram inline button pressed     → DECIDED(allow|deny), kill terminal-notifier subprocess
  total_timeout_seconds exceeded     → DECIDED per timeout_policy, cancel both

MCP tool call unblocks → returns {"decision": "allow|deny"} to Claude Code
```

### Concurrency model

Three goroutines per request all send to a single `chan Decision` (capacity 1). The first write wins. A shared `context.CancelFunc` shuts down the other two. No mutex needed for the decision itself.

Context cancellation is cooperative. Goroutines blocked on network I/O (Telegram long-poll, terminal-notifier subprocess) will unblock when the underlying I/O completes or when the connection is closed by the cancellation path. For Telegram, closing the HTTP client unblocks the poll. For terminal-notifier, killing the subprocess unblocks the stdout reader. Rapid duplicate Telegram button presses (callback race) are handled by the channel: the second write is silently dropped.

### Daemon shutdown (SIGTERM/SIGINT)

On receiving SIGTERM or SIGINT the daemon:
1. Stops accepting new MCP connections
2. Applies `timeout_policy` to all currently pending requests (those that have not yet received any decision) immediately — writes to each `Decision` channel
3. Waits up to 5 seconds for in-flight MCP tool responses to be sent back to Claude Code
4. Logs any requests whose responses were not flushed within the 5s window, then exits

Requests that had already received a decision but whose MCP response had not yet been delivered when SIGTERM fires are treated the same as network-dropped connections from Claude Code's perspective — Claude Code will receive a connection error and fall back to its built-in console prompt.

---

## Components

### `cc` binary (unified — daemon + CLI)

A single Go binary at `cmd/cc/` serves all purposes. launchd invokes `cc daemon` to run the long-running process. The developer uses `cc on` and `cc off` for daily toggling.

**Subcommands:**
- `cc daemon` — starts the daemon (launchd entrypoint); handles signals, HTTP + MCP SSE server, Telegram long-poll loop
- `cc on` — verifies daemon is running, injects `permissionPromptTool` into settings, prints restart reminder
- `cc off` — removes `permissionPromptTool` from settings, prints restart reminder

The `cc daemon` subcommand is a long-running Go process installed as a launchd service (`~/Library/LaunchAgents/com.vokomarov.cc-approvals.plist`). Serves two protocols on a single port:

- **MCP over SSE** at `/mcp` — registers the `request_permission` tool; multiple Claude Code sessions connect as independent MCP clients. Each connection is assigned an internal connection ID by the MCP server. `session_id` values in requests are treated as opaque strings; duplicate `session_id` values from different connections are treated as distinct requests with separate UUIDs. If an SSE connection drops mid-request, the request's context is cancelled; pending goroutines clean up and the request is removed from the store (effectively applying `timeout_policy`).
- **HTTP** at `/health` — liveness check used by `cc on` before modifying settings. Returns `HTTP 200` with body `{"status":"ok"}`. Checks daemon process liveness only — does not probe Telegram connectivity.

Owns all runtime state: pending approval map, Telegram bot long-poll loop, terminal-notifier subprocess lifecycle.

**launchd restart policy:** `KeepAlive = true`, `ThrottleInterval = 10` seconds. The daemon is automatically restarted if it crashes. launchd plist invokes `cc daemon`.

`cc on` and `cc off` read/write JSON safely (parse → mutate → write atomically via temp file + rename), never clobbering other settings keys. If `settings.json` is malformed, the command aborts with an error and leaves the original file untouched.

### MCP Tool: `request_permission`

**`permissionPromptTool` value written by `cc on`:**
```
mcp__cc-approvals__request_permission
```

Input (from Claude Code):
```json
{
  "tool_name": "Bash",
  "tool_input": { "command": "rm -rf ./tmp" },
  "session_id": "abc123"
}
```

`session_id` is a string identifier provided by Claude Code per session. `tool_input` is the raw JSON object as provided by Claude Code. `ProjectPath` in the `ApprovalRequest` struct is populated as the current working directory of the daemon process (which reflects the project directory if the daemon is launched from within the project, or is omitted from notification display if not meaningful).

Output (to Claude Code):
```json
{ "decision": "allow" }
```
or
```json
{ "decision": "deny" }
```

The tool call blocks synchronously until a decision arrives or `total_timeout_seconds` fires. There is no additional MCP-level request timeout — `total_timeout_seconds` is the hard ceiling. Claude Code is never blocked indefinitely because: (a) if the daemon is not running, the SSE connection fails at session start before any tool call is made, causing Claude Code to fall back to its console prompt immediately; (b) if the daemon crashes while a request is in-flight, the SSE connection drop gives Claude Code an immediate transport error on the pending tool call, which it handles as a session reconnection event.

### Telegram Bot

Created via BotFather. The daemon holds the bot token and target chat ID. Uses long-polling (`getUpdates` with a 30s timeout parameter). The long-poll loop runs in a single dedicated goroutine for the lifetime of the daemon; it is not per-request. On network failure, the loop logs the error and retries after a 5-second backoff (no exponential backoff — this is a personal tool with one user). The daemon does not validate Telegram connectivity at startup; failures are discovered at runtime when the first message is sent.

Each pending approval produces one Telegram message with an inline keyboard; callback data encodes the request UUID and decision so multiple concurrent messages work independently.

**Callback data format:** `"approve:{uuid}"` or `"deny:{uuid}"` (max 44 bytes — well within Telegram's 64-byte limit for a 36-char UUID).

**No retry on failure:** If the Telegram send fails (network error, bad token, rate limit), the goroutine logs the error and exits. The request proceeds to `total_timeout_seconds`, at which point `timeout_policy` is applied. No retry loop.

**Message template:** Configurable via `telegram.message_template` in `config.yaml` using Go `text/template` syntax. Available template variables:

| Variable | Value |
|---|---|
| `{{.SessionID}}` | Session identifier from Claude Code |
| `{{.ToolName}}` | Tool being requested (e.g. `Bash`, `Write`) |
| `{{.ToolInput}}` | Full `tool_input` JSON string (truncated to 3800 chars + `...[truncated]` if over limit) |
| `{{.CreatedAt}}` | Request timestamp (formatted as `15:04:05`) |

Default template:
```
🔐 Claude Code Approval Required

Session: {{.SessionID}}
Tool:    {{.ToolName}}
Input:
` + "```" + `
{{.ToolInput}}
` + "```" + `

Waiting for response...
```

Inline buttons: `✅ Approve` | `❌ Deny`

**tool_input size handling:** `{{.ToolInput}}` is pre-truncated to 3800 characters before template rendering (leaving headroom for the surrounding template text within Telegram's 4096-character message limit), with `...[truncated]` appended if truncated. The full raw JSON is always stored in the `ApprovalRequest` struct.

### macOS Notification (terminal-notifier)

Prerequisite: `brew install terminal-notifier`

Invocation (constructed dynamically from config):
```bash
terminal-notifier \
  -title "Claude Code – Bash" \
  -message "rm -rf ./tmp" \
  -actions "Approve,Deny" \
  -activate "com.jetbrains.phpstorm" \
  -timeout 20
```

- `-timeout` is calculated as `telegram_notification_seconds - macos_notification_seconds` (default 20s). The notification auto-dismisses after this duration without registering a decision — the Telegram goroutine then takes over at t=30s.
- `-activate` fires only on body (title/message area) click → focuses PHPStorm
- Action buttons (Approve/Deny) dismiss the notification, write the button label to stdout, and do not focus any app
- The daemon reads stdout from the subprocess to detect which button was pressed
- If the notification is dismissed without interaction (auto-timeout or swiped away), the subprocess exits with no output; the daemon treats this as no decision and the Telegram timer continues

**Tool input truncation for macOS notification `-message`:** Maximum 200 characters of the stringified `tool_input` JSON. If the string exceeds 200 characters it is truncated at 197 characters and appended with `...`. This applies only to the macOS notification; Telegram always receives the full content.

---

## Configuration

**`~/.config/cc-approvals/config.yaml`**

```yaml
telegram:
  bot_token: "YOUR_BOT_TOKEN"
  chat_id: 123456789
  message_template: ""  # optional; leave empty to use built-in default template

timeouts:
  macos_notification_seconds: 15   # 0 = skip macOS notification entirely
  telegram_notification_seconds: 30 # 0 = skip Telegram notification entirely
  total_timeout_seconds: 300        # hard ceiling; timeout_policy applied after this
  timeout_policy: deny              # deny | approve

macos:
  phpstorm_bundle_id: "com.jetbrains.phpstorm"  # activated on notification body click

daemon:
  port: 9753

paths:
  claude_settings: "~/.dotfiles/config/claude/settings.json"  # modified by cc on/off
```

**Validation rules:**
- `macos_notification_seconds`: 0 (disabled) or >= 1
- `telegram_notification_seconds`: 0 (disabled) or >= 1
- If both are non-zero, `telegram_notification_seconds` must exceed `macos_notification_seconds` by at least 5 seconds (ensures macOS notification is visible before auto-dismissing)
- `total_timeout_seconds` must be > 0 and greater than whichever non-zero notification timeout is largest
- `timeout_policy` must be one of `deny` or `approve`
- `bot_token` and `chat_id` must be non-empty (even if `telegram_notification_seconds = 0`, they are required — bot connectivity may be used for future features)
- `port` must be a valid TCP port (1–65535); daemon exits with a clear error on startup if the port is already in use
- `message_template`: if non-empty, must be a valid Go `text/template` string; daemon validates at startup and exits if the template fails to parse

---

## Claude Code Settings (when `cc on` is active)

**`~/.dotfiles/config/claude/settings.json`** (symlinked to `~/.claude/settings.json`):

```json
{
  "mcpServers": {
    "cc-approvals": {
      "type": "sse",
      "url": "http://localhost:9753/mcp"
    }
  },
  "permissionPromptTool": "mcp__cc-approvals__request_permission"
}
```

---

## Go Project Structure

**Repository:** `~/go/src/github.com/vokomarov/claude-code-approvals`

```
claude-code-approvals/
├── cmd/
│   └── cc/
│       └── main.go              # single entrypoint: subcommands daemon | on | off
├── internal/
│   ├── mcp/
│   │   └── handler.go           # MCP server setup, request_permission tool registration
│   ├── approvals/
│   │   ├── store.go             # concurrent map[string]*ApprovalRequest, CRUD
│   │   └── machine.go           # timer goroutines, state transitions, Decision channel
│   ├── notifier/
│   │   └── macos.go             # terminal-notifier subprocess spawn, stdout reader
│   ├── telegram/
│   │   └── bot.go               # long-poll loop, send message, route callback by UUID
│   └── config/
│       └── config.go            # load/validate ~/.config/cc-approvals/config.yaml
├── launchd/
│   └── com.vokomarov.cc-approvals.plist  # invokes: cc daemon
├── config.example.yaml
├── go.mod                       # module: github.com/vokomarov/claude-code-approvals
└── go.sum
```

### Core data types

```go
type ApprovalRequest struct {
    ID          string
    SessionID   string
    ToolName    string
    ToolInput   string        // raw JSON string; truncated to 200 chars for macOS, full for Telegram
    ProjectPath string        // working directory at daemon start; may be empty
    CreatedAt   time.Time
    Decision    chan Decision  // capacity 1; first write wins
    Cancel      context.CancelFunc
}

type Decision struct {
    Value  string // "allow" | "deny"
    Source string // "macos" | "telegram" | "timeout"
}
```

---

## Error Handling

| Failure | Behaviour |
|---|---|
| Daemon not running at session start | MCP SSE connection fails; Claude Code falls back to built-in console Y/N prompt |
| `terminal-notifier` not installed | Daemon logs warning at startup; macOS notification phase skipped (same behaviour as `macos_notification_seconds=0`); Telegram fires at t=`telegram_notification_seconds` if non-zero |
| Telegram send fails (any reason) | Goroutine logs error and exits; request proceeds to `total_timeout_seconds`; `timeout_policy` applied; no retry |
| Both `terminal-notifier` missing AND Telegram fails | Daemon logs a warning that both notification channels are unavailable; request proceeds to `total_timeout_seconds`; `timeout_policy` applied silently |
| macOS + Telegram both respond simultaneously | `chan Decision` capacity 1 ensures first write wins; second write is silently dropped; context cancellation stops the slower goroutine |
| Rapid duplicate Telegram button presses | Second callback arrives after channel is already written; silently dropped |
| macOS notification dismissed without interaction | terminal-notifier exits with no stdout output; daemon treats as no decision; Telegram timer continues normally |
| Claude Code SSE connection drops mid-request | Request context cancelled; goroutines clean up; request removed from store; Claude Code side receives connection error and falls back to console prompt for that session |
| Daemon receives SIGTERM | Applies `timeout_policy` to all undecided pending requests immediately; waits up to 5s for in-flight responses to flush; logs any unflushed request IDs; exits |
| Port already in use at daemon startup | Daemon logs a clear error (`port 9753 already in use`) and exits with code 1 |
| `cc on` / `cc off` with daemon not running | Prints a warning; still modifies settings.json; advises user to start daemon |
| Malformed settings.json | `cc on/off` aborts with error; original file untouched |
| Concurrent write to settings.json | Write is atomic (write to `.tmp` file, then `os.Rename`); last writer wins; no partial-write corruption |

---

## Installation (one-time)

```bash
# 1. Install prerequisite
brew install terminal-notifier

# 2. Create Telegram bot via BotFather, note token + chat ID

# 3. Build and install binary
cd ~/go/src/github.com/vokomarov/claude-code-approvals
go install ./cmd/cc

# 4. Add ~/go/bin to PATH (in ~/.dotfiles/shell/.zshrc)
export PATH="$HOME/go/bin:$PATH"

# 5. Install launchd service
cp launchd/com.vokomarov.cc-approvals.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.vokomarov.cc-approvals.plist

# 6. Configure
mkdir -p ~/.config/cc-approvals
cp config.example.yaml ~/.config/cc-approvals/config.yaml
# Edit config.yaml: set bot_token, chat_id
```

### Daily usage

```bash
cc on    # before leaving laptop or switching to another window
         # then start or restart Claude Code session in PHPStorm

cc off   # when back at laptop
         # then start or restart Claude Code session in PHPStorm
```

---

## Logging

The daemon logs structured JSON to stdout. launchd captures this via the system log. Log levels: `info`, `warn`, `error`. No log file configuration — consumers use `log stream --predicate 'subsystem == "com.vokomarov.cc-approvals"'` or `launchctl log` to observe output. Logged events include: request received, decision made (with source), notification sent/failed, goroutine cleanup, and startup/shutdown lifecycle.

---

## Key Dependencies (Go)

| Package | Purpose |
|---|---|
| `github.com/mark3labs/mcp-go` | MCP server (SSE transport, tool registration) |
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | Telegram Bot API client, long-polling |
| `gopkg.in/yaml.v3` | Config file parsing |
| Standard library only for everything else | HTTP server, subprocess management, concurrency |
