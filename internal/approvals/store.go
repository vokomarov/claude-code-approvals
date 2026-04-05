package approvals

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// NewRequest creates a new ApprovalRequest with a unique UUID and a ready Decision channel.
func NewRequest(sessionID, toolName, toolInput string) *ApprovalRequest {
	_, cancel := context.WithCancel(context.Background())
	return &ApprovalRequest{
		ID:        newUUID(),
		SessionID: sessionID,
		ToolName:  toolName,
		ToolInput: toolInput,
		CreatedAt: time.Now(),
		Decision:  make(chan Decision, 1),
		Cancel:    cancel,
	}
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Store is a thread-safe map of pending ApprovalRequests keyed by UUID.
type Store struct {
	mu   sync.RWMutex
	reqs map[string]*ApprovalRequest
	wake chan struct{} // signalled when the store transitions from empty to non-empty
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{
		reqs: make(map[string]*ApprovalRequest),
		wake: make(chan struct{}, 1),
	}
}

// Add inserts a request into the store.
// If the store was empty, it signals WaitForPending waiters.
func (s *Store) Add(req *ApprovalRequest) {
	s.mu.Lock()
	wasEmpty := len(s.reqs) == 0
	s.reqs[req.ID] = req
	s.mu.Unlock()

	if wasEmpty {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// Get retrieves a request by ID.
func (s *Store) Get(id string) (*ApprovalRequest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.reqs[id]
	return req, ok
}

// Delete removes a request from the store.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reqs, id)
}

// All returns a snapshot of all pending requests (used during shutdown).
func (s *Store) All() []*ApprovalRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ApprovalRequest, 0, len(s.reqs))
	for _, req := range s.reqs {
		out = append(out, req)
	}
	return out
}

// WaitForPending blocks until the store has at least one pending request or ctx is done.
// Returns ctx.Err() if the context is cancelled while waiting.
func (s *Store) WaitForPending(ctx context.Context) error {
	s.mu.RLock()
	hasPending := len(s.reqs) > 0
	s.mu.RUnlock()
	if hasPending {
		return nil
	}

	select {
	case <-s.wake:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
