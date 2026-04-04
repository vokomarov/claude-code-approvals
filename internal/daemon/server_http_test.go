package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/approvals"
	"github.com/vokomarov/claude-code-approvals/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Telegram: config.Telegram{BotToken: "tok", ChatID: 1},
		Timeouts: config.Timeouts{
			MacosNotificationSeconds:    0,
			TelegramNotificationSeconds: 0,
			TotalTimeoutSeconds:         300,
			TimeoutPolicy:               "deny",
		},
		Daemon: config.Daemon{Port: 0},
		MacOS:  config.MacOS{PhpStormBundleID: "com.test"},
		Paths:  config.Paths{ClaudeSettings: t.TempDir() + "/settings.json"},
	}
}

func TestHealthHandler(t *testing.T) {
	srv, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body)
	}
}

func TestPermissionReturns204WhenDisabled(t *testing.T) {
	srv, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	srv.enabled.Store(false)
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()

	body := `{"tool_name":"Bash","tool_input":{},"session_id":"s1","cwd":"/tmp"}`
	resp, err := http.Post(ts.URL+"/api/permission", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestPermissionBlocksUntilDecision(t *testing.T) {
	srv, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()

	type result struct {
		resp *http.Response
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		body := `{"tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"s1","cwd":"/tmp"}`
		resp, err := http.Post(ts.URL+"/api/permission", "application/json", strings.NewReader(body))
		resultCh <- result{resp, err}
	}()

	// Wait for request to appear in the store, then inject a decision.
	var req *approvals.ApprovalRequest
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if all := srv.store.All(); len(all) > 0 {
			req = all[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if req == nil {
		t.Fatal("request never appeared in store")
	}
	req.Decision <- approvals.Decision{Value: "allow", Source: "test"}

	res := <-resultCh
	if res.err != nil {
		t.Fatal(res.err)
	}
	defer res.resp.Body.Close()
	if res.resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.resp.StatusCode)
	}
	var out map[string]string
	json.NewDecoder(res.resp.Body).Decode(&out)
	if out["decision"] != "allow" {
		t.Errorf("expected allow, got %v", out["decision"])
	}
}

// TestShutdownAlwaysDeniesPendingRequests verifies that shutdown() sends
// Decision{Value:"deny"} to all pending requests regardless of timeout_policy.
func TestShutdownAlwaysDeniesPendingRequests(t *testing.T) {
	// Use timeout_policy "approve" to confirm shutdown overrides it with "deny".
	cfg := testConfig(t)
	cfg.Timeouts.TimeoutPolicy = "approve"

	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Wire up a real httptest server so srv.httpServer.Shutdown won't panic.
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()
	srv.httpServer = ts.Config

	// Add a pending request directly to the store.
	req := approvals.NewRequest("sess-shutdown", "Bash", `{"command":"ls"}`)
	srv.store.Add(req)

	// Run shutdown in a goroutine; it should complete promptly.
	done := make(chan struct{})
	go func() {
		srv.shutdown()
		close(done)
	}()

	// Collect the decision sent by shutdown.
	var got approvals.Decision
	select {
	case got = <-req.Decision:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for shutdown decision")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not complete in time")
	}

	if got.Value != "deny" {
		t.Errorf("expected deny, got %q", got.Value)
	}
	if got.Source != "timeout" {
		t.Errorf("expected source=timeout, got %q", got.Source)
	}
}

func TestEnableDisableToggle(t *testing.T) {
	srv, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.handler(context.Background()))
	defer ts.Close()

	client := &http.Client{}

	// Disable — permission requests should get 204.
	resp, _ := client.Post(ts.URL+"/api/disable", "", nil)
	resp.Body.Close()
	if srv.enabled.Load() {
		t.Error("expected daemon to be disabled")
	}

	body := `{"tool_name":"Bash","tool_input":{},"session_id":"s2","cwd":"/tmp"}`
	resp, _ = http.Post(ts.URL+"/api/permission", "application/json", strings.NewReader(body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 when disabled, got %d", resp.StatusCode)
	}

	// Re-enable.
	resp, _ = client.Post(ts.URL+"/api/enable", "", nil)
	resp.Body.Close()
	if !srv.enabled.Load() {
		t.Error("expected daemon to be enabled")
	}
}
