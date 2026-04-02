package approvals_test

import (
	"testing"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/approvals"
)

func TestMachineTimeoutPolicyDeny(t *testing.T) {
	req := approvals.NewRequest("s", "Bash", "{}")

	approvals.RunMachine(req, approvals.MachineOpts{
		MacosSeconds:    0,
		TelegramSeconds: 0,
		TotalSeconds:    1,
		TimeoutPolicy:   "deny",
		OnMacos:         func(r *approvals.ApprovalRequest) {},
		OnTelegram:      func(r *approvals.ApprovalRequest) {},
	})

	select {
	case d := <-req.Decision:
		if d.Value != "deny" {
			t.Errorf("expected deny, got %q", d.Value)
		}
		if d.Source != "timeout" {
			t.Errorf("expected source=timeout, got %q", d.Source)
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout: machine did not produce decision")
	}
}

func TestMachineTimeoutPolicyApprove(t *testing.T) {
	req := approvals.NewRequest("s", "Bash", "{}")

	approvals.RunMachine(req, approvals.MachineOpts{
		MacosSeconds:    0,
		TelegramSeconds: 0,
		TotalSeconds:    1,
		TimeoutPolicy:   "approve",
		OnMacos:         func(r *approvals.ApprovalRequest) {},
		OnTelegram:      func(r *approvals.ApprovalRequest) {},
	})

	select {
	case d := <-req.Decision:
		if d.Value != "approve" {
			t.Errorf("expected approve, got %q", d.Value)
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout: machine did not produce decision")
	}
}

func TestMachineMacosCallbackFires(t *testing.T) {
	req := approvals.NewRequest("s", "Bash", "{}")
	fired := make(chan struct{}, 1)

	approvals.RunMachine(req, approvals.MachineOpts{
		MacosSeconds:    1,
		TelegramSeconds: 10,
		TotalSeconds:    30,
		TimeoutPolicy:   "deny",
		OnMacos:         func(r *approvals.ApprovalRequest) { fired <- struct{}{} },
		OnTelegram:      func(r *approvals.ApprovalRequest) {},
	})

	select {
	case <-fired:
		// success
	case <-time.After(3 * time.Second):
		t.Error("OnMacos was not called within expected time")
	}
	req.Cancel()
}

func TestMachineTelegramCallbackFires(t *testing.T) {
	req := approvals.NewRequest("s", "Bash", "{}")
	fired := make(chan struct{}, 1)

	approvals.RunMachine(req, approvals.MachineOpts{
		MacosSeconds:    0,
		TelegramSeconds: 1,
		TotalSeconds:    30,
		TimeoutPolicy:   "deny",
		OnMacos:         func(r *approvals.ApprovalRequest) {},
		OnTelegram:      func(r *approvals.ApprovalRequest) { fired <- struct{}{} },
	})

	select {
	case <-fired:
		// success
	case <-time.After(3 * time.Second):
		t.Error("OnTelegram was not called within expected time")
	}
	req.Cancel()
}

func TestMachineMacosSkippedWhenZero(t *testing.T) {
	req := approvals.NewRequest("s", "Bash", "{}")
	macosCalledAfterCancel := false

	approvals.RunMachine(req, approvals.MachineOpts{
		MacosSeconds:    0,
		TelegramSeconds: 0,
		TotalSeconds:    1,
		TimeoutPolicy:   "deny",
		OnMacos:         func(r *approvals.ApprovalRequest) { macosCalledAfterCancel = true },
		OnTelegram:      func(r *approvals.ApprovalRequest) {},
	})

	<-req.Decision // wait for timeout
	if macosCalledAfterCancel {
		t.Error("OnMacos should not be called when MacosSeconds=0")
	}
}

func TestMachineFirstWriteWins(t *testing.T) {
	req := approvals.NewRequest("s", "Bash", "{}")

	approvals.RunMachine(req, approvals.MachineOpts{
		MacosSeconds:    0,
		TelegramSeconds: 0,
		TotalSeconds:    60,
		TimeoutPolicy:   "deny",
		OnMacos:         func(r *approvals.ApprovalRequest) {},
		OnTelegram:      func(r *approvals.ApprovalRequest) {},
	})

	// External decision arrives (simulating macOS notifier)
	req.Decision <- approvals.Decision{Value: "allow", Source: "macos"}

	select {
	case d := <-req.Decision:
		if d.Value != "allow" {
			t.Errorf("expected allow, got %q", d.Value)
		}
		if d.Source != "macos" {
			t.Errorf("expected source=macos, got %q", d.Source)
		}
	case <-time.After(time.Second):
		t.Error("timeout reading decision")
	}
	req.Cancel()
}
