package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silent-knight19/antares/internal/client"
	"github.com/silent-knight19/antares/internal/clock"
)

const (
	defaultPollInterval = 250 * time.Millisecond
	defaultTargetTTL    = 5 * time.Second
	probeTimeout        = 500 * time.Millisecond
	maxBodyBytes        = 1 << 20
	maxScanCount        = 1000
	maxReplyBytes       = 8 << 20
)

// Config is the bridge's process configuration.
type Config struct {
	Addr         string
	AntaresAddr  string
	RedisAddr    string
	WebDir       string
	Version      string
	PollInterval time.Duration
}

// Bridge talks RESP to Antares (and optionally real Redis) and serves the
// Observatory REST/SSE surface plus the built SPA (ADR-013, ADR-015).
type Bridge struct {
	cfg      Config
	clock    clock.Clock
	antares  *target
	redis    *target
	ctx      context.Context
	cancel   context.CancelFunc
	handlers http.Handler
}

// New builds a bridge. Targets are dialed lazily so the bridge can outlive a
// server restart; Close stops in-flight SSE streams and connections.
func New(cfg Config) *Bridge {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:6390"
	}
	if cfg.AntaresAddr == "" {
		cfg.AntaresAddr = "127.0.0.1:6380"
	}
	if cfg.WebDir == "" {
		cfg.WebDir = "web/dist"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	clk := clock.Real{}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bridge{
		cfg:    cfg,
		clock:  clk,
		ctx:    ctx,
		cancel: cancel,
		antares: &target{
			addr:    cfg.AntaresAddr,
			timeout: defaultTargetTTL,
			clock:   clk,
		},
	}
	if cfg.RedisAddr != "" {
		b.redis = &target{addr: cfg.RedisAddr, timeout: defaultTargetTTL, clock: clk}
	}
	b.handlers = b.routes()
	return b
}

// Close stops streams and closes both target connections.
func (b *Bridge) Close() {
	b.cancel()
	b.antares.close()
	if b.redis != nil {
		b.redis.close()
	}
}

// Handler returns the bridge's HTTP handler.
func (b *Bridge) Handler() http.Handler { return b.handlers }

func (b *Bridge) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", b.handleHealth)
	mux.HandleFunc("POST /api/exec", b.handleExec)
	mux.HandleFunc("GET /api/keys", b.handleKeys)
	mux.HandleFunc("GET /api/info", b.handleInfo)
	mux.HandleFunc("GET /api/events/stream", b.handleEvents)
	mux.Handle("/", http.HandlerFunc(b.serveStatic))
	return b.guard(mux)
}

// guard keeps the local console out of reach of DNS rebinding and cross-site
// form posts: the Host must be loopback (or the host the bridge was told to
// bind), and a mutating request with an Origin must come from the same host.
func (b *Bridge) guard(next http.Handler) http.Handler {
	allowedHost := ""
	if host, _, err := net.SplitHostPort(b.cfg.Addr); err == nil {
		allowedHost = strings.Trim(host, "[]")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if !loopbackHost(host) && host != allowedHost {
			writeError(w, http.StatusForbidden, errors.New("forbidden host"))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					writeError(w, http.StatusForbidden, errors.New("forbidden origin"))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// target is a lazily connected client. A transport failure drops the
// connection so the next command re-dials, which keeps the console usable
// across an Antares restart.
type target struct {
	addr    string
	timeout time.Duration
	clock   clock.Clock

	mu sync.Mutex
	c  *client.Client
}

var errTargetNotConfigured = errors.New("target not configured")

func (t *target) exec(ctx context.Context, args ...string) (client.Result, error) {
	if len(args) == 0 {
		return client.Result{}, errors.New("empty command")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c, err := t.conn(ctx)
	if err != nil {
		return client.Result{}, err
	}
	res, err := c.Do(ctx, args...)
	if err != nil {
		t.drop()
	}
	return res, err
}

// execMany sends all commands in one pipeline and returns their replies in
// order, saving one round trip per command.
func (t *target) execMany(ctx context.Context, commands ...[]string) ([]client.Reply, error) {
	if len(commands) == 0 {
		return nil, nil
	}
	batch := make([][][]byte, len(commands))
	for i, args := range commands {
		batch[i] = make([][]byte, len(args))
		for j, arg := range args {
			batch[i][j] = []byte(arg)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c, err := t.conn(ctx)
	if err != nil {
		return nil, err
	}
	replies, err := c.Pipeline(ctx, batch...)
	if err != nil {
		t.drop()
	}
	return replies, err
}

func (t *target) conn(ctx context.Context) (*client.Client, error) {
	if t.c == nil {
		c, err := client.Dial(ctx, t.addr, client.WithTimeout(t.timeout), client.WithClock(t.clock))
		if err != nil {
			return nil, err
		}
		t.c = c
	}
	return t.c, nil
}

func (t *target) drop() {
	if t.c != nil {
		_ = t.c.Close()
		t.c = nil
	}
}

func (t *target) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.c != nil {
		_ = t.c.Close()
		t.c = nil
	}
}

func (b *Bridge) target(name string) (*target, error) {
	switch strings.ToLower(name) {
	case "", "antares":
		return b.antares, nil
	case "redis":
		if b.redis == nil {
			return nil, errTargetNotConfigured
		}
		return b.redis, nil
	default:
		return nil, fmt.Errorf("unknown target %q", name)
	}
}

// replyJSON is the JSON rendering of a RESP2 reply.
type replyJSON struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

func toReplyJSON(r client.Reply) replyJSON {
	switch r.Kind {
	case client.KindArray:
		items := make([]replyJSON, len(r.Array))
		for i, item := range r.Array {
			items[i] = toReplyJSON(item)
		}
		return replyJSON{Type: string(client.KindArray), Value: items}
	case client.KindInteger:
		return replyJSON{Type: string(client.KindInteger), Value: r.Int}
	case client.KindNull, client.KindNullArray:
		return replyJSON{Type: string(client.KindNull), Value: nil}
	default:
		return replyJSON{Type: string(r.Kind), Value: r.Text}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (b *Bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	antares := b.probe(r.Context(), b.antares)
	redis := probeResult{Error: "not configured"}
	if b.redis != nil {
		redis = b.probe(r.Context(), b.redis)
	}
	status := "ok"
	if !antares.OK {
		status = "down"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       status,
		"version":      b.cfg.Version,
		"antares":      antares,
		"antares_addr": b.cfg.AntaresAddr,
		"redis":        redis,
		"at_ms":        b.clock.Now().UnixMilli(),
	})
}

type probeResult struct {
	OK        bool    `json:"ok"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
	Error     string  `json:"error,omitempty"`
}

func (b *Bridge) probe(parent context.Context, t *target) probeResult {
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()
	res, err := t.exec(ctx, "PING")
	if err != nil {
		return probeResult{Error: err.Error()}
	}
	if res.Reply.Kind == client.KindError {
		return probeResult{Error: res.Reply.Text}
	}
	return probeResult{OK: true, LatencyMS: ms(res.Latency)}
}

type execBody struct {
	Args   []string `json:"args"`
	Target string   `json:"target"`
}

func (b *Bridge) handleExec(w http.ResponseWriter, r *http.Request) {
	var body execBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(body.Args) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("args must not be empty"))
		return
	}
	t, err := b.target(body.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultTargetTTL)
	defer cancel()
	res, err := t.exec(ctx, body.Args...)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if len(res.Raw) > maxReplyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("reply is %d bytes, limit %d; use a narrower command", len(res.Raw), maxReplyBytes))
		return
	}
	name := strings.ToLower(body.Target)
	if name == "" {
		name = "antares"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target":     name,
		"reply":      toReplyJSON(res.Reply),
		"request":    string(res.Request),
		"raw":        string(res.Raw),
		"latency_ms": ms(res.Latency),
	})
}

type keyInfo struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	TTLMS   int64  `json:"ttl_ms"`
	Preview string `json:"preview"`
}

func (b *Bridge) handleKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cursor := q.Get("cursor")
	if cursor == "" {
		cursor = "0"
	}
	count := 100
	if raw := q.Get("count"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxScanCount {
			writeError(w, http.StatusBadRequest, fmt.Errorf("count must be between 1 and %d", maxScanCount))
			return
		}
		count = n
	}
	match := q.Get("match")
	if match == "" {
		match = "*"
	}
	args := []string{"SCAN", cursor, "COUNT", strconv.Itoa(count), "MATCH", match}
	if kind := q.Get("type"); kind != "" {
		args = append(args, "TYPE", kind)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*defaultTargetTTL)
	defer cancel()
	res, err := b.antares.exec(ctx, args...)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if res.Reply.Kind == client.KindError {
		writeError(w, http.StatusBadGateway, errors.New(res.Reply.Text))
		return
	}
	if res.Reply.Kind != client.KindArray || len(res.Reply.Array) != 2 ||
		res.Reply.Array[0].Kind != client.KindBulk || res.Reply.Array[1].Kind != client.KindArray {
		writeError(w, http.StatusBadGateway, errors.New("malformed SCAN reply"))
		return
	}
	items := res.Reply.Array[1].Array
	keys := make([]keyInfo, 0, len(items))
	commands := make([][]string, 0, len(items)*2)
	for _, item := range items {
		if item.Kind != client.KindBulk {
			writeError(w, http.StatusBadGateway, errors.New("malformed SCAN key"))
			return
		}
		keys = append(keys, keyInfo{Key: item.Text, Type: "none", TTLMS: -2})
		commands = append(commands, []string{"TYPE", item.Text}, []string{"PTTL", item.Text})
	}

	replies, err := b.antares.execMany(ctx, commands...)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if len(replies) != len(commands) {
		writeError(w, http.StatusBadGateway, errors.New("malformed pipeline reply"))
		return
	}
	sized := make([]int, 0, len(keys))
	commands = commands[:0]
	for i := range keys {
		if replies[2*i].Kind == client.KindSimple {
			keys[i].Type = replies[2*i].Text
		}
		if replies[2*i+1].Kind == client.KindInteger {
			keys[i].TTLMS = replies[2*i+1].Int
		}
		if command, ok := previewCommand(keys[i].Type); ok {
			sized = append(sized, i)
			commands = append(commands, []string{command, keys[i].Key})
		}
	}
	replies, err = b.antares.execMany(ctx, commands...)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if len(replies) != len(commands) {
		writeError(w, http.StatusBadGateway, errors.New("malformed pipeline reply"))
		return
	}
	for i, index := range sized {
		if replies[i].Kind == client.KindInteger {
			keys[index].Preview = fmt.Sprintf("%d %s", replies[i].Int, previewUnit(keys[index].Type))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cursor": res.Reply.Array[0].Text,
		"keys":   keys,
	})
}

func previewCommand(kind string) (string, bool) {
	switch kind {
	case "string":
		return "STRLEN", true
	case "list":
		return "LLEN", true
	case "hash":
		return "HLEN", true
	case "set":
		return "SCARD", true
	default:
		return "", false
	}
}

func previewUnit(kind string) string {
	switch kind {
	case "string":
		return "B"
	case "list":
		return "items"
	case "hash":
		return "fields"
	case "set":
		return "members"
	default:
		return ""
	}
}

func (b *Bridge) handleInfo(w http.ResponseWriter, r *http.Request) {
	args := []string{"INFO"}
	if section := r.URL.Query().Get("section"); section != "" {
		args = append(args, section)
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultTargetTTL)
	defer cancel()
	res, err := b.antares.exec(ctx, args...)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if res.Reply.Kind != client.KindBulk {
		writeError(w, http.StatusBadGateway, errors.New("malformed INFO reply"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"raw":    res.Reply.Text,
		"fields": parseInfoFields(res.Reply.Text),
	})
}

func (b *Bridge) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, "retry: 2000\n\n")
	writeSSE(w, flusher, "hello", 0, map[string]any{"status": "ok", "at_ms": b.clock.Now().UnixMilli()})

	ticker := time.NewTicker(b.cfg.PollInterval)
	defer ticker.Stop()
	seq := 0
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			seq++
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			res, err := b.antares.exec(ctx, "INFO")
			cancel()
			if err != nil {
				writeSSE(w, flusher, "status", seq, map[string]any{"ok": false, "error": err.Error()})
				continue
			}
			if res.Reply.Kind != client.KindBulk {
				writeSSE(w, flusher, "status", seq, map[string]any{"ok": false, "error": "malformed INFO reply"})
				continue
			}
			writeSSE(w, flusher, "info", seq, map[string]any{
				"fields": parseInfoFields(res.Reply.Text),
				"at_ms":  b.clock.Now().UnixMilli(),
			})
		}
	}
}

func writeSSE(w io.Writer, flusher http.Flusher, event string, id int, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(`{"error":"encode failed"}`)
	}
	if id > 0 {
		fmt.Fprintf(w, "id: %d\n", id)
	}
	fmt.Fprintf(w, "event: %s\n", event)
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

func parseInfoFields(raw string) map[string]string {
	fields := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[name] = value
	}
	return fields
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON body: trailing data")
	}
	return nil
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

func (b *Bridge) serveStatic(w http.ResponseWriter, r *http.Request) {
	if b.cfg.WebDir != "" {
		clean := filepath.Clean("/" + r.URL.Path)
		rel := strings.TrimPrefix(clean, "/")
		full := filepath.Join(b.cfg.WebDir, rel)
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			http.ServeFile(w, r, full)
			return
		}
		index := filepath.Join(b.cfg.WebDir, "index.html")
		if _, err := os.Stat(index); err == nil && wantsHTML(r) {
			http.ServeFile(w, r, index)
			return
		}
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, placeholderHTML)
}

func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// placeholderHTML is served until U3 produces web/dist. It keeps U2 testable
// with curl and makes the missing build obvious in a browser.
const placeholderHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Antares Observatory</title>
<style>body{background:#0b0d10;color:#e6e9ef;font:14px/1.5 ui-sans-serif,system-ui;margin:0;display:grid;place-items:center;height:100vh}
main{max-width:560px;padding:24px}code{color:#ff5a3c}a{color:#5cc8ff}</style></head>
<body><main><h1>Antares Observatory</h1>
<p>The bridge is running, but the SPA build was not found.</p>
<p>Run <code>make showcase-dev</code> to build the UI, or check
<a href="/api/health">/api/health</a> directly.</p></main></body></html>
`
