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
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{reqs: make(map[string]*ApprovalRequest)}
}

// Add inserts a request into the store.
func (s *Store) Add(req *ApprovalRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs[req.ID] = req
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
