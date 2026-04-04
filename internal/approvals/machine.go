package approvals

import (
	"context"
	"time"
)

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
	TimeoutPolicy   string // "approve" | "deny" — only consulted when TotalSeconds > 0
	OnMacos         func(*ApprovalRequest)
	OnTelegram      func(*ApprovalRequest)
}

// RunMachine starts background goroutines for a request and returns immediately.
//
// Goroutines stop when req.Cancel() is called. The caller (daemon handler) is
// responsible for calling req.Cancel() after reading req.Decision, which stops
// any pending notification goroutines. The total-timeout goroutine is the only
// goroutine within the machine that writes to req.Decision; notification callbacks
// (OnMacos, OnTelegram) write to it externally via their notifier implementations.
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
}
