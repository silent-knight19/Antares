package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestBridgeSSRF verifies that the target field in /api/exec cannot be coerced
// to dial arbitrary host:port combinations (e.g. cloud metadata 169.254.169.254).
func TestBridgeSSRF(t *testing.T) {
	_, ts := newTestBridge(t)

	ssrfTargets := []string{
		"169.254.169.254:80",
		"http://169.254.169.254",
		"internal-service:8080",
		"127.0.0.1:22",
		"attacker.com:6379",
	}

	for _, target := range ssrfTargets {
		payload, _ := json.Marshal(execBody{
			Args:   []string{"PING"},
			Target: target,
		})
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/exec", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", ts.URL)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /api/exec with target %q: %v", target, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for SSRF target %q, got: %d", target, resp.StatusCode)
		}

		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if !strings.Contains(errResp["error"], "unknown target") {
			t.Fatalf("expected 'unknown target' error for %q, got: %v", target, errResp)
		}
	}
}

// TestBridgeCSRF tests origin validation edge cases, including 'Origin: null'
// and cross-site Host header spoofing.
func TestBridgeCSRF(t *testing.T) {
	_, ts := newTestBridge(t)
	payload, _ := json.Marshal(execBody{Args: []string{"PING"}})

	// 1. Origin: null (sandboxed iframe / data: URI)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/exec", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "null")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST with Origin: null: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Origin: null status = %d, want 403 Forbidden", resp.StatusCode)
	}

	// 2. Host: attacker.com
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/exec", bytes.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Host = "attacker.com"
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST with Host: attacker.com: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("Host: attacker.com status = %d, want 403 Forbidden", resp2.StatusCode)
	}

	// 3. Sec-Fetch-Site: cross-site
	req3, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/exec", bytes.NewReader(payload))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Sec-Fetch-Site", "cross-site")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("POST with Sec-Fetch-Site: cross-site: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Fatalf("Sec-Fetch-Site: cross-site status = %d, want 403 Forbidden", resp3.StatusCode)
	}
}

// TestBridgeDemoModeGate verifies that destructive lab endpoints require DEMO_MODE=1.
func TestBridgeDemoModeGate(t *testing.T) {
	// Ensure DEMO_MODE is disabled
	t.Setenv("DEMO_MODE", "")
	_, ts := newTestBridge(t)

	endpoints := []string{
		"/api/lab/durability/simulate",
		"/api/lab/replication/action",
		"/api/lab/transaction",
	}

	for _, ep := range endpoints {
		resp, err := http.Post(ts.URL+ep, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST %s: %v", ep, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("endpoint %s returned status %d, want 403 Forbidden when DEMO_MODE is not set", ep, resp.StatusCode)
		}
	}
}

