# claude-code-approvals

A macOS daemon that intercepts [Claude Code](https://claude.ai/code) permission prompts and escalates them to your phone when you're away from your laptop.

When Claude Code asks for permission to run a command, this tool can:
1. Show a **macOS notification** with Approve / Deny buttons (at t=15s by default)
2. Send a **Telegram message** with inline buttons if the notification goes unanswered (at t=30s)
3. Auto-apply a policy (`deny` or `approve`) after a hard timeout

Toggle the whole system on or off with a single command — no Claude Code restart required beyond the session boundary.

---

## How it works

```
Claude Code (PHPStorm / any IDE)
    │
    │  MCP over SSE  (when cc-approvals is enabled)
    ▼
claude-code-approvals daemon  (localhost:9753)
    │
    ├── t=15s ──► macOS notification  (terminal-notifier)
    │               [Approve] [Deny]
    │
    └── t=30s ──► Telegram message
                    [✅ Approve] [❌ Deny]
```

The daemon runs as a launchd service and exposes an MCP server that Claude Code connects to as its `permissionPromptTool`. Each permission request blocks until a decision arrives from any channel — the first response wins.

---

## Requirements

- macOS (Apple Silicon or Intel)
- Go 1.21+
- [terminal-notifier](https://github.com/julienXX/terminal-notifier): `brew install terminal-notifier`
- A Telegram bot token and chat ID (see [setup](#telegram-bot-setup))

---

## Installation

### 1. Clone and build

```bash
git clone https://github.com/vokomarov/claude-code-approvals.git ~/go/src/github.com/vokomarov/claude-code-approvals
cd ~/go/src/github.com/vokomarov/claude-code-approvals
make install
```

Make sure `~/go/bin` is in your `PATH`:

```bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
```

### 2. Configure

```bash
cp config.example.yaml config.yml
```

Edit `config.yml` and set your Telegram credentials at minimum:

```yaml
telegram:
  bot_token: "YOUR_BOT_TOKEN"
  chat_id: 123456789

paths:
  claude_settings: "~/.claude/settings.json"  # adjust to your actual path
```

`config.yml` is gitignored — it stays local.

### 3. Install the launchd service

```bash
make service-install
```

Verify the daemon is running:

```bash
make health
# → {"status":"ok"}
```

### 4. Add the MCP server to Claude Code settings

```bash
claude-code-approvals on
```

This injects the MCP server entry and `permissionPromptTool` into your Claude Code `settings.json`. Start a new Claude Code session to activate.

---

## Telegram bot setup

1. Open Telegram and message [@BotFather](https://t.me/BotFather) → `/newbot`
2. Copy the bot token into `config.yml` under `telegram.bot_token`
3. Send any message to your new bot, then open:
   ```
   https://api.telegram.org/bot<TOKEN>/getUpdates
   ```
   Read `result[0].message.chat.id` and set it as `telegram.chat_id`

---

## Daily usage

```bash
claude-code-approvals on    # enable — then start a new Claude Code session
claude-code-approvals off   # disable — then start a new Claude Code session
```

| Situation | Command |
|---|---|
| Check daemon is alive | `make health` |
| View logs | `make logs` |
| View error logs | `make logs-err` |
| Restart daemon | `make service-reload` |
| Stop daemon | `make service-stop` |

After making code changes:

```bash
make reinstall   # rebuild, update plist, restart daemon, health check
```

---

## Configuration reference

| Key | Default | Description |
|---|---|---|
| `timeouts.macos_notification_seconds` | `15` | Delay before macOS notification. `0` = skip |
| `timeouts.telegram_notification_seconds` | `30` | Delay before Telegram message. `0` = skip |
| `timeouts.total_timeout_seconds` | `300` | Hard ceiling; applies `timeout_policy` after this |
| `timeouts.timeout_policy` | `deny` | Decision on hard timeout: `deny` or `approve` |
| `macos.phpstorm_bundle_id` | `com.jetbrains.phpstorm` | App focused on notification body click |
| `telegram.message_template` | (built-in) | Go `text/template`; vars: `.SessionID` `.ToolName` `.ToolInput` `.CreatedAt` |
| `paths.claude_settings` | — | Path to Claude Code `settings.json` modified by `on`/`off` |
| `daemon.port` | `9753` | Port for MCP SSE server and health endpoint |

**Validation rules:**
- If both notification timeouts are non-zero, `telegram_notification_seconds` must exceed `macos_notification_seconds` by at least 5
- `total_timeout_seconds` must exceed the largest non-zero notification timeout
- `telegram.bot_token` and `chat_id` are always required

**Timeout-only mode** (no notifications, auto-deny after 60s):

```yaml
timeouts:
  macos_notification_seconds: 0
  telegram_notification_seconds: 0
  total_timeout_seconds: 60
  timeout_policy: deny
```

---

## Claude Code settings

When `claude-code-approvals on` is active, your `settings.json` contains:

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

`claude-code-approvals off` removes both keys atomically.

---

## Development

```bash
make test        # run tests
make test-race   # run tests with race detector
make vet         # go vet
make lint        # golangci-lint (requires golangci-lint installed)
```

---

## License

MIT
