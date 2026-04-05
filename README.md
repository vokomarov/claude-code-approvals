# claude-code-approvals

A macOS daemon that intercepts [Claude Code](https://claude.ai/code) permission prompts and escalates them to your phone when you're away from your laptop.

When Claude Code asks for permission to run a command, this tool can:
1. Show a **macOS notification** with Approve / Deny buttons (at t=15s by default)
2. Send a **Telegram message** with inline buttons if the notification goes unanswered (at t=30s)
3. Auto-apply a policy (`deny` or `approve`) after a hard timeout — or wait indefinitely if no timeout is set

Toggle the whole system on or off with a single command — no Claude Code restart required.

---

## How it works

```
Claude Code (per permission request)
    │
    │  spawns: claude-code-approvals hook
    ▼
hook process  (reads stdin JSON, POSTs to daemon)
    │
    │  POST /api/permission  (blocking)
    ▼
claude-code-approvals daemon  (localhost:9753)
    │
    ├── t=15s ──► macOS notification  (alerter)
    │               [Approve] [Deny]
    │
    └── t=30s ──► Telegram message
                    [✅ Approve] [❌ Deny]
```

The daemon runs as a launchd service. Claude Code invokes the `hook` subcommand as a subprocess for each permission request; the hook blocks until the daemon returns a decision. When the daemon is disabled (`cc off`), it returns immediately and Claude Code falls back to its built-in interactive prompt.

---

## Requirements

- macOS (Apple Silicon or Intel)
- Go 1.21+
- [alerter](https://github.com/vjeantet/alerter): `brew install vjeantet/tap/alerter`
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

### 3. Configure the launchd service file

Open `launchd/com.vokomarov.cc-approvals.plist` and update the paths to match your environment:

```xml
<!-- Path to the installed binary -->
<string>/Users/YOUR_USERNAME/go/bin/claude-code-approvals</string>

<!-- Working directory (where config.yml lives) -->
<string>/Users/YOUR_USERNAME/go/src/github.com/vokomarov/claude-code-approvals</string>

<!-- Log output paths -->
<string>/Users/YOUR_USERNAME/go/src/github.com/vokomarov/claude-code-approvals/logs/cc-approvals.log</string>
<string>/Users/YOUR_USERNAME/go/src/github.com/vokomarov/claude-code-approvals/logs/cc-approvals-error.log</string>
```

Replace `YOUR_USERNAME` with your macOS username (`whoami`).

### 4. Install the launchd service

```bash
make service-install
```

Verify the daemon is running:

```bash
make health
# → {"status":"ok"}
```

### 5. Register the hook in Claude Code settings (one-time)

```bash
claude-code-approvals install
```

This writes the `PermissionRequest` hook entry to your Claude Code `settings.json`. You only need to do this once — it persists across daemon restarts and session restarts.

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
claude-code-approvals on    # enable interception (no session restart needed)
claude-code-approvals off   # disable — Claude Code uses its built-in prompt
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
| `timeouts.total_timeout_seconds` | `300` | Hard ceiling in seconds; `0` = wait indefinitely (no timeout) |
| `timeouts.timeout_policy` | `deny` | Decision on hard timeout: `deny` or `approve`. Only used when `total_timeout_seconds > 0` |
| `telegram.message_template` | (built-in) | Go `text/template`; vars: `.SessionID` `.ToolName` `.ToolInput` `.CreatedAt` |
| `paths.claude_settings` | — | Path to Claude Code `settings.json` modified by `install`/`uninstall` |
| `daemon.port` | `9753` | Port for HTTP API and health endpoint |

**Validation rules:**
- If both notification timeouts are non-zero, `telegram_notification_seconds` must exceed `macos_notification_seconds` by at least 5
- `total_timeout_seconds: 0` means wait indefinitely; any positive value must exceed the largest non-zero notification timeout
- `timeout_policy` is required only when `total_timeout_seconds > 0`
- On daemon shutdown, all pending requests are always denied regardless of `timeout_policy`
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

After `claude-code-approvals install`, your `settings.json` contains:

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/you/go/bin/claude-code-approvals hook"
          }
        ]
      }
    ]
  }
}
```

`claude-code-approvals uninstall` removes this entry. The `on`/`off` commands do not modify `settings.json` — they toggle the daemon's state via HTTP.

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
