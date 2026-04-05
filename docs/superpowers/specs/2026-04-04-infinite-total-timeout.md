# Infinite Total Timeout Support

**Date:** 2026-04-04
**Status:** Approved

## Summary

Allow `total_timeout_seconds: 0` in config to mean "no hard ceiling — wait indefinitely for a user decision." Previously, zero was rejected by validation. This also makes `timeout_policy` optional when there is no total timeout, and hardens the shutdown path to always deny pending requests.

## Motivation

Some users want approval requests to wait as long as needed — e.g., when they are away and will respond via Telegram whenever they return. A forced ceiling of 5 minutes (or any positive value) auto-denies requests that could have been approved. Zero is the natural sentinel given that `macos_notification_seconds: 0` and `telegram_notification_seconds: 0` already mean "skip this channel."

## Design

### Config (`internal/config/config.go`)

- **`total_timeout_seconds: 0`** = infinite wait. Any positive value = hard ceiling in seconds.
- **`timeout_policy`** is only required when `total_timeout_seconds > 0`. When total is zero, the field is unused at runtime and validation skips the check.
- The existing rule `total > maxNotification` is only enforced when `total > 0`.
- The existing rule `total > 0` is removed; `total >= 0` is the new floor.

Validation pseudocode:
```
if total > 0:
    require timeout_policy in {"deny", "approve"}
    if maxNotification > 0 and total <= maxNotification:
        error
else (total == 0):
    skip both checks above
```

### Machine (`internal/approvals/machine.go`)

`RunMachine` skips the total-timeout goroutine when `MachineOpts.TotalSeconds == 0`. Notification timers (`MacosSeconds`, `TelegramSeconds`) are unaffected and fire at their configured delays regardless of the total-timeout setting.

`MachineOpts.TotalSeconds` comment updated: "0 = no hard ceiling; >0 = hard ceiling in seconds."
`MachineOpts.TimeoutPolicy` comment updated: "only consulted when TotalSeconds > 0."

### Shutdown (`internal/daemon/server.go`)

`Server.shutdown()` always sends `Decision{Value: "deny", Source: "timeout"}` to all pending requests. Previously it used `s.cfg.Timeouts.TimeoutPolicy`, which was wrong when `total_timeout_seconds: 0` (no policy configured) and potentially surprising when it auto-approved on daemon restart.

### Documentation

- `config.example.yaml`: add comments clarifying zero semantics for `total_timeout_seconds` and note that `timeout_policy` is only relevant when total > 0.
- `CLAUDE.md`: update the config constraints in the Architecture section and remove the stale Known Gaps entry if resolved.

## Invariants Preserved

- First write to `req.Decision` wins (cap-1 channel). Shutdown's deny is still a non-blocking send.
- Notification goroutines are still cancelled by `req.Cancel()` after a decision is reached.
- `macos_notification_seconds` and `telegram_notification_seconds` semantics are unchanged (delay before trigger, 0 = skip).

## Out of Scope

- Persisting pending requests across daemon restarts.
- Any UI indication that a request is waiting indefinitely.
