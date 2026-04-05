package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunWritesAllowDecision(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"decision":"allow"}`)
	}))
	defer ts.Close()

	in := strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"},"cwd":"/tmp"}`)
	var out bytes.Buffer
	run(in, &out, ts.URL, 5*time.Second)

	var got map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, out.String())
	}
	hso, ok := got["hookSpecificOutput"].(map[string]interface{})
	if !ok {
		t.Fatal("hookSpecificOutput missing or not an object")
	}
	if hso["hookEventName"] != "PermissionRequest" {
		t.Errorf("wrong hookEventName: %v", hso["hookEventName"])
	}
	dec, ok := hso["decision"].(map[string]interface{})
	if !ok {
		t.Fatal("decision missing or not an object")
	}
	if dec["behavior"] != "allow" {
		t.Errorf("expected allow, got %v", dec["behavior"])
	}
}

func TestRunWritesDenyDecision(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"decision":"deny"}`)
	}))
	defer ts.Close()

	in := strings.NewReader(`{"session_id":"s1","tool_name":"Write","tool_input":{"path":"/etc/hosts"},"cwd":"/tmp"}`)
	var out bytes.Buffer
	run(in, &out, ts.URL, 5*time.Second)

	var got map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, out.String())
	}
	hso := got["hookSpecificOutput"].(map[string]interface{})
	dec := hso["decision"].(map[string]interface{})
	if dec["behavior"] != "deny" {
		t.Errorf("expected deny, got %v", dec["behavior"])
	}
}

func TestRunSilentWhenDaemonDown(t *testing.T) {
	in := strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{},"cwd":"/tmp"}`)
	var out bytes.Buffer
	// Port 1 is privileged — no server will be listening there.
	run(in, &out, "http://localhost:1", 200*time.Millisecond)
	if out.Len() > 0 {
		t.Errorf("expected no output when daemon is down, got: %s", out.String())
	}
}

func TestRunSilentOn204(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	in := strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{},"cwd":"/tmp"}`)
	var out bytes.Buffer
	run(in, &out, ts.URL, 5*time.Second)
	if out.Len() > 0 {
		t.Errorf("expected no output on 204, got: %s", out.String())
	}
}

func TestRunForwardsFullStdinToDaemon(t *testing.T) {
	var received bytes.Buffer
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = received.ReadFrom(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"decision":"allow"}`)
	}))
	defer ts.Close()

	in := strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls -la"},"cwd":"/home/user"}`)
	var out bytes.Buffer
	run(in, &out, ts.URL, 5*time.Second)

	var body map[string]interface{}
	if err := json.Unmarshal(received.Bytes(), &body); err != nil {
		t.Fatalf("daemon received invalid JSON: %v", err)
	}
	if body["tool_name"] != "Bash" {
		t.Errorf("tool_name not forwarded: %v", body["tool_name"])
	}
	if body["session_id"] != "s1" {
		t.Errorf("session_id not forwarded: %v", body["session_id"])
	}
	if body["cwd"] != "/home/user" {
		t.Errorf("cwd not forwarded: %v", body["cwd"])
	}
}

func TestRunSilentOnInvalidStdin(t *testing.T) {
	in := strings.NewReader(`{bad json}`)
	var out bytes.Buffer
	run(in, &out, "http://localhost:9753", 5*time.Second)
	if out.Len() > 0 {
		t.Errorf("expected no output on invalid stdin, got: %s", out.String())
	}
}
