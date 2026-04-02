package notifier_test

import (
	"testing"

	"github.com/vokomarov/claude-code-approvals/internal/notifier"
)

func TestTruncateToolInput(t *testing.T) {
	long := string(make([]byte, 300))
	for i := range long {
		long = long[:i] + "a" + long[i+1:]
	}
	result := notifier.TruncateForMacOS(long)
	if len(result) > 200 {
		t.Errorf("expected <= 200 chars, got %d", len(result))
	}
	if result[len(result)-3:] != "..." {
		t.Error("expected truncated string to end with '...'")
	}
}

func TestTruncateShortInput(t *testing.T) {
	short := "ls -la"
	result := notifier.TruncateForMacOS(short)
	if result != short {
		t.Errorf("short input should not be modified, got %q", result)
	}
}

func TestNotifierAvailable(t *testing.T) {
	// Just checks the availability check doesn't panic.
	_ = notifier.IsAvailable()
}
