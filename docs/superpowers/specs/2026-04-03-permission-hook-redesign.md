# PermissionRequest Hook Redesign
**Date:** 2026-04-03
**Status:** Approved

---

## Problem

The current implementation injects `"permissionPromptTool"` as a key in Claude Code's `settings.json` and runs a full MCP server over SSE in the daemon. This key is undocumented and not confirmed to work in Claude Code's IDE plugin sessions. The `--permission-prompt-tool` CLI flag (the documented MCP-based approach) targets CI/headless invocations, not interactive sessions.

The `PermissionRequest` hook is the documented, stable mechanism for intercepting permission requests from `settings.json` in all session types.

---

## Goals

- Replace `permissionPromptTool` + MCP server with the `PermissionRequest` hook
- Hook is permanently registered in `settings.json` (one-time `install`); toggling is daemon-side, no session restart required
- `cc off` disables processing without stopping the daemon; hook degrades gracefully to Claude Code's built-in interactive prompt
- Preserve all existing approval logic: state machine, macOS notifications, Telegram bot, concurrency model

## Non-Goals

- Changing notification behaviour or timing
- Changing config file format
- Supporting multiple simultaneous enable/disable callers (personal tool)

---

## Architecture

```
Claude Code permission request
  → spawns: claude-code-approvals hook  (per request, no persistent connection)
  → hook reads JSON from stdin
  → hook POSTs to http://localhost:{port}/api/permission  (blocking HTTP)
  → daemon checks enabled flag
      ├── disabled → 204 No Content immediately
      └── enabled  → creates ApprovalRequest, starts state machine
                          ├── t=macosSeconds  → terminal-notifier
                          ├── t=telegramSeconds → Telegram inline buttons
                          └── t=totalSeconds  → timeout_policy applied
                      first decision → responds 200 {"decision":"allow|deny"}
  → hook receives response
      ├── 200 → writes hookSpecificOutput JSON to stdout, exits 0
      └── 204 / error → writes nothing to stdout, exits 0
  → Claude Code reads stdout
      ├── valid hookSpecificOutput → uses hook decision
      └── empty → falls back to built-in interactive prompt
```

---

## CLI Subcommands

| Subcommand | What it does |
|---|---|
| `daemon` | Start the daemon (launchd entrypoint); starts in **enabled** state |
| `install` | One-time: writes `hooks.PermissionRequest` entry into `settings.json`; no MCP entries |
| `uninstall` | Removes the hooks entry from `settings.json` |
| `on` | `POST /api/enable` to the running daemon; idempotent |
| `off` | `POST /api/disable` to the running daemon; daemon stays up; idempotent |
| `hook` | Reads stdin JSON, POSTs to `/api/permission`, writes decision to stdout |

`on`/`off` no longer touch `settings.json`. `install`/`uninstall` are run once at setup.

---

## Daemon HTTP API

| Endpoint | Behaviour |
|---|---|
| `GET /health` | Returns `{"status":"ok"}`; unchanged |
| `POST /api/permission` | When enabled: creates `ApprovalRequest`, blocks until decided, returns `{"decision":"allow\|deny"}`. When disabled: returns `HTTP 204 No Content` immediately. |
| `POST /api/enable` | Sets enabled flag to true; idempotent |
| `POST /api/disable` | Sets enabled flag to false; idempotent |

Enabled state is held in an `atomic.Bool` — no mutex needed in the hot path. State is in-memory only; daemon restart resets to enabled. For a permanent off, use `launchctl stop`.

---

## `hook` Subcommand

**Stdin** (from Claude Code):
```json
{
  "tool_name": "Bash",
  "tool_input": { "command": "rm -rf ./tmp" },
  "session_id": "abc123",
  "cwd": "/Users/vovan/project"
}
```

**Request body** to `/api/permission`:
Same JSON as received on stdin (forwarded as-is).

**Stdout** on `200 OK`:
```json
{
  "hookSpecificOutput": {
    "hookEventName": "PermissionRequest",
    "decision": { "behavior": "allow" }
  }
}
```

Or with `"behavior": "deny"` accordingly.

**Stdout on `204` or any error:** nothing — hook exits 0 silently, Claude Code uses interactive prompt.

The hook loads the config file (same path as the daemon: `~/.config/cc-approvals/config.yaml`) to read `daemon.port` and `timeouts.total_timeout_seconds`. It sets a client-side HTTP timeout of `total_timeout_seconds + 5s`. If config cannot be loaded, it uses defaults (port 9753, timeout 310s) and proceeds.

---

## `settings.json` Shape (after `cc install`)

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/vovan/go/bin/claude-code-approvals hook"
          }
        ]
      }
    ]
  }
}
```

The binary path is resolved at `install` time using `os.Executable()`. Existing keys in `settings.json` are preserved (atomic write via temp file + rename, same as before).

---

## Package Changes

### Removed

| Item | Reason |
|---|---|
| `internal/mcp/` | MCP server no longer used |
| `github.com/mark3labs/mcp-go` | Dependency removed from `go.mod` |

### Changed

| Package | Change |
|---|---|
| `internal/settings` | `Enable(path, port)` → `Install(path, binaryPath string)`; writes hooks entry. `Disable(path)` → `Uninstall(path)`; removes hooks entry. `mcpServers` and `permissionPromptTool` keys gone. |
| `internal/daemon/server.go` | Remove MCP server wiring. Add `atomic.Bool` enabled flag. Add handlers for `/api/permission`, `/api/enable`, `/api/disable`. |
| `cmd/claude-code-approvals/main.go` | Add `hook`, `install`, `uninstall` subcommands. `on` → HTTP `POST /api/enable`. `off` → HTTP `POST /api/disable`. |

### Unchanged

| Package | Reason |
|---|---|
| `internal/approvals/` (store, machine, types) | Core approval logic untouched |
| `internal/notifier/` | macOS notification unchanged |
| `internal/telegram/` | Telegram bot unchanged |
| `internal/config/` | Config format unchanged |

---

## Error Handling

| Failure | Behaviour |
|---|---|
| Daemon not running when hook is called | Connection refused; hook exits 0 with no output; Claude Code interactive prompt |
| Daemon disabled (`cc off`) | 204 returned immediately; hook exits 0 with no output; Claude Code interactive prompt |
| Hook HTTP timeout (daemon frozen) | Hook exits 0 with no output; Claude Code interactive prompt |
| `cc on`/`cc off` with daemon not running | Command fails with clear error: `daemon not reachable at localhost:{port}` |
| `cc install` with malformed `settings.json` | Aborts with error; original file untouched |
| Concurrent `cc on`/`cc off` calls | `atomic.Bool` ensures safe concurrent flag updates |
| Daemon SIGTERM with requests in-flight | Unchanged: applies `timeout_policy` to all pending requests, waits up to 5s to flush responses |

---

## Installation Flow (updated)

```bash
# 1. Build and install binary
go install ./cmd/claude-code-approvals

# 2. Configure
cp config.example.yaml ~/.config/cc-approvals/config.yaml
# edit: set bot_token, chat_id

# 3. Install launchd service (daemon starts enabled)
cp launchd/com.vokomarov.cc-approvals.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.vokomarov.cc-approvals.plist

# 4. Register hook in Claude Code settings (one-time)
claude-code-approvals install

# Daily usage — no session restart needed
claude-code-approvals off   # stop intercepting (daemon stays up)
claude-code-approvals on    # resume intercepting
```
