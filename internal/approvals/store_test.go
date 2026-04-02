package approvals_test

import (
	"sync"
	"testing"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/approvals"
)

func TestStoreAddAndGet(t *testing.T) {
	store := approvals.NewStore()
	req := approvals.NewRequest("sess1", "Bash", `{"command":"ls"}`)

	store.Add(req)
	got, ok := store.Get(req.ID)
	if !ok {
		t.Fatal("expected to find request")
	}
	if got.ID != req.ID {
		t.Errorf("got ID %q, want %q", got.ID, req.ID)
	}
}

func TestStoreDelete(t *testing.T) {
	store := approvals.NewStore()
	req := approvals.NewRequest("sess1", "Bash", `{"command":"ls"}`)
	store.Add(req)
	store.Delete(req.ID)
	_, ok := store.Get(req.ID)
	if ok {
		t.Error("expected request to be deleted")
	}
}

func TestStoreUniqueIDs(t *testing.T) {
	store := approvals.NewStore()
	r1 := approvals.NewRequest("s", "Bash", "{}")
	r2 := approvals.NewRequest("s", "Bash", "{}")
	store.Add(r1)
	store.Add(r2)
	if r1.ID == r2.ID {
		t.Error("expected unique IDs")
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	store := approvals.NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := approvals.NewRequest("s", "Bash", "{}")
			store.Add(req)
			store.Get(req.ID)
			store.Delete(req.ID)
		}()
	}
	wg.Wait()
}

func TestRequestDecisionChannel(t *testing.T) {
	req := approvals.NewRequest("s", "Bash", "{}")
	if cap(req.Decision) != 1 {
		t.Errorf("Decision channel should have capacity 1, got %d", cap(req.Decision))
	}
	req.Decision <- approvals.Decision{Value: "allow", Source: "test"}
	select {
	case d := <-req.Decision:
		if d.Value != "allow" {
			t.Errorf("expected allow, got %q", d.Value)
		}
	case <-time.After(time.Second):
		t.Error("timeout reading decision")
	}
}

func TestStoreAll(t *testing.T) {
	store := approvals.NewStore()
	r1 := approvals.NewRequest("s1", "Bash", "{}")
	r2 := approvals.NewRequest("s2", "Write", "{}")
	store.Add(r1)
	store.Add(r2)
	all := store.All()
	if len(all) != 2 {
		t.Errorf("expected 2 requests, got %d", len(all))
	}
}
