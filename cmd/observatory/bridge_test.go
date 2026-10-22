package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/server"
)

func startAntares(t *testing.T) string {
	t.Helper()
	exec := server.NewExecutor(nil, nil)
	t.Cleanup(exec.Close)
	srv := server.New(server.Config{Addr: "127.0.0.1:0", Stats: exec.Stats()}, exec.Handle)
	if err := srv.Start(); err != nil {
		t.Fatalf("start antares: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv.Addr().String()
}

func newTestBridge(t *testing.T) (*Bridge, *httptest.Server) {
	t.Helper()
	b := New(Config{AntaresAddr: startAntares(t), Version: "test", PollInterval: 10 * time.Millisecond})
	t.Cleanup(b.Close)
	ts := httptest.NewServer(b.Handler())
	t.Cleanup(ts.Close)
	return b, ts
}

func getJSON(t *testing.T, url string, dst any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

func postExec(t *testing.T, base string, body execBody) (int, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(body)
	resp, err := http.Post(base+"/api/exec", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /api/exec: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode exec: %v", err)
	}
	return resp.StatusCode, out
}

func TestHealth(t *testing.T) {
	_, ts := newTestBridge(t)
	var body struct {
		Status      string      `json:"status"`
		AntaresAddr string      `json:"antares_addr"`
		Antares     probeResult `json:"antares"`
		Redis       probeResult `json:"redis"`
	}
	if code := getJSON(t, ts.URL+"/api/health", &body); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body.Status != "ok" || !body.Antares.OK {
		t.Fatalf("health = %+v", body)
	}
	if body.AntaresAddr == "" {
		t.Fatalf("antares_addr is empty")
	}
	if body.Redis.OK || body.Redis.Error != "not configured" {
		t.Fatalf("redis = %+v", body.Redis)
	}
}

func TestExec(t *testing.T) {
	_, ts := newTestBridge(t)

	code, out := postExec(t, ts.URL, execBody{Args: []string{"PING"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, out)
	}
	reply := out["reply"].(map[string]any)
	if reply["type"] != "simple" || reply["value"] != "PONG" {
		t.Fatalf("reply = %v", reply)
	}
	if out["request"] != "*1\r\n$4\r\nPING\r\n" || out["raw"] != "+PONG\r\n" {
		t.Fatalf("wire = %q / %q", out["request"], out["raw"])
	}

	code, out = postExec(t, ts.URL, execBody{Args: []string{"SET", "k", "v"}})
	if code != http.StatusOK {
		t.Fatalf("SET status = %d", code)
	}
	_, out = postExec(t, ts.URL, execBody{Args: []string{"GET", "k"}})
	reply = out["reply"].(map[string]any)
	if reply["type"] != "bulk" || reply["value"] != "v" {
		t.Fatalf("GET reply = %v", reply)
	}

	code, out = postExec(t, ts.URL, execBody{Args: []string{"INCR", "k"}})
	if code != http.StatusOK {
		t.Fatalf("INCR status = %d", code)
	}
	reply = out["reply"].(map[string]any)
	if reply["type"] != "error" {
		t.Fatalf("INCR reply = %v", reply)
	}

	code, _ = postExec(t, ts.URL, execBody{Args: []string{"PING"}, Target: "redis"})
	if code != http.StatusBadRequest {
		t.Fatalf("redis target status = %d", code)
	}
	code, _ = postExec(t, ts.URL, execBody{})
	if code != http.StatusBadRequest {
		t.Fatalf("empty args status = %d", code)
	}
}

func TestKeys(t *testing.T) {
	_, ts := newTestBridge(t)

	postExec(t, ts.URL, execBody{Args: []string{"SET", "user:1", "Alice", "PX", "60000"}})
	postExec(t, ts.URL, execBody{Args: []string{"RPUSH", "queue", "a", "b"}})
	postExec(t, ts.URL, execBody{Args: []string{"HSET", "session", "name", "Ada"}})
	postExec(t, ts.URL, execBody{Args: []string{"SADD", "tags", "go"}})

	var body struct {
		Cursor string    `json:"cursor"`
		Keys   []keyInfo `json:"keys"`
	}
	if code := getJSON(t, ts.URL+"/api/keys?count=10&match=*", &body); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	byKey := make(map[string]keyInfo, len(body.Keys))
	for _, k := range body.Keys {
		byKey[k.Key] = k
	}
	for key, wantType := range map[string]string{"user:1": "string", "queue": "list", "session": "hash", "tags": "set"} {
		info, ok := byKey[key]
		if !ok || info.Type != wantType {
			t.Fatalf("%s = %+v (present %v), want type %s", key, info, ok, wantType)
		}
	}
	if byKey["user:1"].TTLMS <= 0 {
		t.Fatalf("user:1 ttl = %d, want > 0", byKey["user:1"].TTLMS)
	}
	if byKey["queue"].TTLMS != -1 {
		t.Fatalf("queue ttl = %d, want -1", byKey["queue"].TTLMS)
	}
	for key, wantPreview := range map[string]string{
		"user:1":  "5 B",
		"queue":   "2 items",
		"session": "1 fields",
		"tags":    "1 members",
	} {
		if got := byKey[key].Preview; got != wantPreview {
			t.Fatalf("%s preview = %q, want %q", key, got, wantPreview)
		}
	}

	if code := getJSON(t, ts.URL+"/api/keys?type=list", &body); code != http.StatusOK {
		t.Fatalf("type filter status = %d", code)
	}
	for _, k := range body.Keys {
		if k.Type != "list" {
			t.Fatalf("type filter returned %+v", k)
		}
	}

	if code := getJSON(t, ts.URL+"/api/keys?count=0", nil); code != http.StatusBadRequest {
		t.Fatalf("count=0 status = %d", code)
	}
}

func TestInfo(t *testing.T) {
	_, ts := newTestBridge(t)
	var body struct {
		Raw    string            `json:"raw"`
		Fields map[string]string `json:"fields"`
	}
	if code := getJSON(t, ts.URL+"/api/info?section=server", &body); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body.Fields["redis_version"] != "7.4.11" {
		t.Fatalf("fields = %+v", body.Fields)
	}
	if !strings.Contains(body.Raw, "# Server") {
		t.Fatalf("raw = %q", body.Raw)
	}
}

func TestEventStream(t *testing.T) {
	_, ts := newTestBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type = %q", got)
	}

	reader := bufio.NewReader(resp.Body)
	var events []string
	for len(events) < 2 {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("read event: %v", err)
		}
		if strings.HasPrefix(line, "event: ") {
			events = append(events, strings.TrimSpace(strings.TrimPrefix(line, "event: ")))
		}
	}
	if len(events) < 2 || events[0] != "hello" {
		t.Fatalf("events = %v", events)
	}
}

func TestStaticPlaceholder(t *testing.T) {
	_, ts := newTestBridge(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Header.Set("Accept", "text/html")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Antares Observatory") {
		t.Fatalf("placeholder = %d %q", resp.StatusCode, body)
	}

	resp, err = http.Get(ts.URL + "/missing.js")
	if err != nil {
		t.Fatalf("GET missing: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", resp.StatusCode)
	}
}

func TestBridgeRejectsForeignHost(t *testing.T) {
	_, ts := newTestBridge(t)
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with foreign Host: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host status = %d, want 403", resp.StatusCode)
	}
}

func TestBridgeRejectsCrossOriginWrite(t *testing.T) {
	_, ts := newTestBridge(t)
	payload, _ := json.Marshal(execBody{Args: []string{"PING"}})

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/exec", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cross-origin POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/exec", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("same-origin POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("same-origin status = %d, want 200", resp.StatusCode)
	}
}

func TestExecRejectsOversizedReply(t *testing.T) {
	b, ts := newTestBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	big := strings.Repeat("x", maxReplyBytes+1)
	if _, err := b.antares.exec(ctx, "SET", "big", big); err != nil {
		t.Fatalf("set big value: %v", err)
	}
	code, out := postExec(t, ts.URL, execBody{Args: []string{"GET", "big"}})
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized GET status = %d, body = %v", code, out)
	}
}
