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

// RunMachine starts background goroutines for a request.
// It returns immediately. Goroutines stop when a decision is written
// to req.Decision or when req.Cancel() is called.
func RunMachine(req *ApprovalRequest, opts MachineOpts) {
	ctx, cancel := context.WithCancel(context.Background())

	// Wrap the request's Cancel so both the machine context and the original cancel fire together.
	origCancel := req.Cancel
	req.Cancel = func() {
		cancel()
		origCancel()
	}

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

	// Total timeout: always runs, writes to Decision channel directly.
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
