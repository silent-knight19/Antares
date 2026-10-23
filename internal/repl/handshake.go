package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

// HandshakeStep records one request-response pair during replication handshake.
type HandshakeStep struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
	Sent string   `json:"sent"`
	Recv string   `json:"recv"`
}

// HandshakeResult contains the outcome of a master handshake.
type HandshakeResult struct {
	MasterReplID     string          `json:"master_replid"`
	MasterReplOffset int64           `json:"master_repl_offset"`
	IsFullResync     bool            `json:"is_full_resync"`
	Trace            []HandshakeStep `json:"trace"`
	Conn             net.Conn        `json:"-"`
	Reader           *bufio.Reader   `json:"-"`
}

// Close closes the underlying connection if open.
func (r *HandshakeResult) Close() error {
	if r.Conn != nil {
		return r.Conn.Close()
	}
	return nil
}

// HandshakeConfig configures a replica-to-master handshake.
type HandshakeConfig struct {
	MasterAddr    string
	ListeningPort int
	ReplID        string
	Offset        int64
	Timeout       time.Duration
	Clock         clock.Clock
}

// PerformHandshake executes the 4-step Redis replication handshake against masterAddr.
// Sequence: PING -> REPLCONF listening-port -> REPLCONF capa -> PSYNC.
func PerformHandshake(ctx context.Context, cfg HandshakeConfig) (*HandshakeResult, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.ReplID == "" {
		cfg.ReplID = "?"
		cfg.Offset = -1
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", cfg.MasterAddr)
	if err != nil {
		return nil, fmt.Errorf("dial master %s: %w", cfg.MasterAddr, err)
	}

	res, err := PerformHandshakeOnConn(ctx, conn, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return res, nil
}

// PerformHandshakeOnConn executes the handshake sequence over an existing net.Conn.
func PerformHandshakeOnConn(ctx context.Context, conn net.Conn, cfg HandshakeConfig) (*HandshakeResult, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.ReplID == "" {
		cfg.ReplID = "?"
		cfg.Offset = -1
	}

	reader := bufio.NewReader(conn)
	trace := make([]HandshakeStep, 0, 4)

	execStep := func(name string, args []string, expectPrefix string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		_ = conn.SetDeadline(cfg.Clock.Now().Add(cfg.Timeout))

		// Encode RESP Array
		wireSent := encodeRESPArray(args)
		if _, wErr := conn.Write([]byte(wireSent)); wErr != nil {
			return "", fmt.Errorf("%s write: %w", name, wErr)
		}

		// Read reply (skipping bare keepalive newlines from master)
		var line string
		for {
			l, rErr := reader.ReadString('\n')
			if rErr != nil {
				return "", fmt.Errorf("%s read: %w", name, rErr)
			}
			if len(l) > 64*1024 {
				return "", fmt.Errorf("%s read line exceeds 64KB limit", name)
			}
			trimmed := strings.TrimRight(l, "\r\n")
			if trimmed == "" {
				// Keepalive newline received from master; reset deadline and continue waiting
				_ = conn.SetDeadline(cfg.Clock.Now().Add(cfg.Timeout))
				continue
			}
			line = trimmed
			break
		}

		step := HandshakeStep{
			Name: name,
			Args: args,
			Sent: wireSent,
			Recv: line,
		}
		trace = append(trace, step)

		if strings.HasPrefix(line, "-") {
			return line, fmt.Errorf("%s error reply: %s", name, strings.TrimPrefix(line, "-"))
		}
		if expectPrefix != "" && !strings.HasPrefix(line, expectPrefix) {
			return line, fmt.Errorf("%s unexpected reply: want prefix %q, got %q", name, expectPrefix, line)
		}

		return line, nil
	}

	// Step 1: PING
	if _, err := execStep("PING", []string{"PING"}, "+PONG"); err != nil {
		return nil, err
	}

	// Step 2: REPLCONF listening-port <port>
	portStr := strconv.Itoa(cfg.ListeningPort)
	if _, err := execStep("REPLCONF_PORT", []string{"REPLCONF", "listening-port", portStr}, "+OK"); err != nil {
		return nil, err
	}

	// Step 3: REPLCONF capa eof capa psync2
	if _, err := execStep("REPLCONF_CAPA", []string{"REPLCONF", "capa", "eof", "capa", "psync2"}, "+OK"); err != nil {
		return nil, err
	}

	// Step 4: PSYNC <replid> <offset>
	offsetStr := strconv.FormatInt(cfg.Offset, 10)
	psyncLine, err := execStep("PSYNC", []string{"PSYNC", cfg.ReplID, offsetStr}, "+")
	if err != nil {
		return nil, err
	}

	res := &HandshakeResult{
		Trace:  trace,
		Conn:   conn,
		Reader: reader,
	}

	if strings.HasPrefix(psyncLine, "+FULLRESYNC ") {
		res.IsFullResync = true
		parts := strings.Split(strings.TrimPrefix(psyncLine, "+FULLRESYNC "), " ")
		if len(parts) >= 2 {
			res.MasterReplID = parts[0]
			if off, pErr := strconv.ParseInt(parts[1], 10, 64); pErr == nil {
				res.MasterReplOffset = off
			}
		}
	} else if strings.HasPrefix(psyncLine, "+CONTINUE") {
		res.IsFullResync = false
		parts := strings.Fields(strings.TrimPrefix(psyncLine, "+CONTINUE"))
		if len(parts) >= 1 && parts[0] != "" {
			res.MasterReplID = parts[0]
		}
	} else {
		return nil, fmt.Errorf("unexpected PSYNC reply: %s", psyncLine)
	}

	_ = conn.SetDeadline(time.Time{})
	return res, nil
}

func encodeRESPArray(args []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	return b.String()
}

// HandshakeCapture simulates reading a real replica protocol capture for testing.
type HandshakeCapture struct {
	Exchanges []HandshakeStep
}

// ReplayHandshake verifies that trace matches an expected capture trace.
func ReplayHandshake(trace []HandshakeStep) error {
	if len(trace) < 4 {
		return errors.New("incomplete handshake: expected 4 steps")
	}
	if trace[0].Name != "PING" || !strings.Contains(trace[0].Recv, "PONG") {
		return fmt.Errorf("step 0 PING failed: got %s", trace[0].Recv)
	}
	if trace[1].Name != "REPLCONF_PORT" || !strings.Contains(trace[1].Recv, "OK") {
		return fmt.Errorf("step 1 REPLCONF port failed: got %s", trace[1].Recv)
	}
	if trace[2].Name != "REPLCONF_CAPA" || !strings.Contains(trace[2].Recv, "OK") {
		return fmt.Errorf("step 2 REPLCONF capa failed: got %s", trace[2].Recv)
	}
	if trace[3].Name != "PSYNC" || (!strings.Contains(trace[3].Recv, "+FULLRESYNC") && !strings.Contains(trace[3].Recv, "+CONTINUE")) {
		return fmt.Errorf("step 3 PSYNC failed: got %s", trace[3].Recv)
	}
	return nil
}
