# Next Steps — Installation & Smoke Test

Follow these steps after cloning or copying the repository to `~/go/src/github.com/vokomarov/claude-code-approvals`.

---

## 1. Install Dependencies

```bash
brew install terminal-notifier
```

---

## 2. Build and Install the Binary

```bash
cd ~/go/src/github.com/vokomarov/claude-code-approvals
go install ./cmd/cc
```

Verify:

```bash
which cc   # → /Users/vokomarov/go/bin/cc
cc         # → prints usage
```

---

## 3. Configure

```bash
mkdir -p ~/.config/cc-approvals
cp config.example.yaml ~/.config/cc-approvals/config.yaml
```

Edit `~/.config/cc-approvals/config.yaml` — at minimum set:

```yaml
telegram:
  bot_token: "YOUR_BOT_TOKEN_FROM_BOTFATHER"
  chat_id: YOUR_CHAT_ID
```

To get a bot token: open Telegram → `@BotFather` → `/newbot`.
To get your chat ID: send any message to your bot, then open `https://api.telegram.org/bot<TOKEN>/getUpdates` in a browser and read `message.chat.id`.

---

## 4. Create the Log Directory

```bash
mkdir -p ~/Library/Logs/cc-approvals
```

---

## 5. Install the launchd Service

```bash
cp launchd/com.vokomarov.cc-approvals.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.vokomarov.cc-approvals.plist
```

Verify it is running:

```bash
sleep 2
curl -s http://localhost:9753/health
# → {"status":"ok"}
```

If the health check fails, check the logs:

```bash
cat ~/Library/Logs/cc-approvals/cc-approvals-error.log
```

---

## 6. Smoke Test the CLI

### Enable notifications

```bash
cc on
```

Expected output:
```
✅ Notifications enabled.
   Settings updated: /Users/vokomarov/.dotfiles/config/claude/settings.json
   Restart your Claude Code session in PHPStorm to apply.
```

Verify the key was injected:

```bash
grep permissionPromptTool ~/.dotfiles/config/claude/settings.json
# → "permissionPromptTool": "mcp__cc-approvals__request_permission"
```

### Disable notifications

```bash
cc off
grep -c permissionPromptTool ~/.dotfiles/config/claude/settings.json
# → 0
```

---

## 7. End-to-End Test

1. Run `cc on`
2. Open PHPStorm and start a new Claude Code session (so the updated `settings.json` is loaded)
3. Ask Claude Code to run a Bash command
4. **Do nothing** for 15 seconds — a macOS native notification should appear with **Approve** / **Deny** buttons
5. Click **Approve** — Claude Code proceeds
6. Alternatively, wait another 15 seconds (t=30s total) — a Telegram message should arrive with inline buttons

---

## 8. Daily Workflow

| Situation | Command |
|---|---|
| Leaving laptop; want mobile approval | `cc on` → start new Claude Code session |
| Back at laptop; want console Y/N | `cc off` → start new Claude Code session |
| Check if daemon is alive | `curl -s http://localhost:9753/health` |
| Stop daemon entirely | `launchctl stop com.vokomarov.cc-approvals` |
| Restart daemon | `launchctl kickstart -k gui/$(id -u)/com.vokomarov.cc-approvals` |
| View daemon logs | `tail -f ~/Library/Logs/cc-approvals/cc-approvals.log` |
| Unload daemon from autostart | `launchctl unload ~/Library/LaunchAgents/com.vokomarov.cc-approvals.plist` |

---

## 9. Updating the Binary

```bash
cd ~/go/src/github.com/vokomarov/claude-code-approvals
git pull
go install ./cmd/cc
launchctl kickstart -k gui/$(id -u)/com.vokomarov.cc-approvals
```

---

## Configuration Reference

Key options in `~/.config/cc-approvals/config.yaml`:

| Key | Default | Effect |
|---|---|---|
| `timeouts.macos_notification_seconds` | 15 | Seconds before macOS notification fires. `0` = skip macOS notifications entirely |
| `timeouts.telegram_notification_seconds` | 30 | Seconds before Telegram notification fires. `0` = skip Telegram notifications entirely |
| `timeouts.total_timeout_seconds` | 300 | Hard ceiling; applies `timeout_policy` after this |
| `timeouts.timeout_policy` | `deny` | Decision applied on hard timeout: `deny` or `approve` |
| `telegram.message_template` | (built-in) | Go `text/template`; variables: `.SessionID` `.ToolName` `.ToolInput` `.CreatedAt` |
| `paths.claude_settings` | `~/.dotfiles/config/claude/settings.json` | Path that `cc on`/`cc off` modifies |

Both-zero example (pure timeout mode — no notifications, just auto-deny after 60s):

```yaml
timeouts:
  macos_notification_seconds: 0
  telegram_notification_seconds: 0
  total_timeout_seconds: 60
  timeout_policy: deny
```
