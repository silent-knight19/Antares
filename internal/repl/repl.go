// Package repl implements REPLICAOF, PSYNC full/partial resync, the backlog
// ring buffer, and the replica apply loop (ADR-007, ADR-010).
package repl

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

// Role describes whether the node is a replication master or a replica.
type Role string

const (
	RoleMaster Role = "master"
	RoleSlave  Role = "slave"
)

// LinkStatus describes the replica-to-master connection state.
type LinkStatus string

const (
	LinkStatusDown       LinkStatus = "down"
	LinkStatusConnecting LinkStatus = "connecting"
	LinkStatusHandshake  LinkStatus = "handshake"
	LinkStatusSync       LinkStatus = "sync"
	LinkStatusUp         LinkStatus = "up"
)

// DefaultBacklogSize is the 1MB replication backlog size specified in PLAN §5.
const DefaultBacklogSize = 1048576

// State tracks replication role, master/replica offsets, and link state.
type State struct {
	mu sync.RWMutex

	role       Role
	masterHost string
	masterPort int
	linkStatus LinkStatus

	masterReplID     string
	masterReplID2    string
	masterReplOffset int64
	secondReplOffset int64

	slaveReplOffset int64
	slaveReadOnly   bool
	slavePriority   int

	connectedSlaves int
	lastIO          time.Time
	syncInProgress  bool

	backlogActive bool
	backlogSize   int64
	backlog       *Backlog

	clock clock.Clock
}

// NewState creates a replication state initialized as master.
func NewState(replID string, clk clock.Clock) *State {
	if clk == nil {
		clk = clock.Real{}
	}
	if replID == "" {
		replID = strings.Repeat("0", 40)
	}
	b := NewBacklog(DefaultBacklogSize)
	return &State{
		role:             RoleMaster,
		linkStatus:       LinkStatusDown,
		masterReplID:     replID,
		masterReplID2:    strings.Repeat("0", 40),
		masterReplOffset: 0,
		secondReplOffset: -1,
		slaveReplOffset:  0,
		slaveReadOnly:    true,
		slavePriority:    100,
		connectedSlaves:  0,
		backlogActive:    true,
		backlogSize:      DefaultBacklogSize,
		backlog:          b,
		clock:            clk,
	}
}

// Backlog returns the underlying replication backlog ring buffer.
func (s *State) Backlog() *Backlog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backlog
}

// FeedBacklog appends payload bytes to the replication backlog, advancing the
// master stream offset and updating the backlog history.
func (s *State) FeedBacklog(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.backlog == nil {
		s.backlog = NewBacklog(s.backlogSize)
		s.backlogActive = true
	}
	_, _ = s.backlog.Write(p)
	s.masterReplOffset = s.backlog.Offset()
}

// CanPartialResync checks if the replica's offset can be satisfied from backlog.
func (s *State) CanPartialResync(reqOffset int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.backlogActive || s.backlog == nil {
		return false
	}
	return s.backlog.CanPartialResync(reqOffset)
}

// BacklogSlice extracts the replication stream from reqOffset + 1 to current offset.
func (s *State) BacklogSlice(reqOffset int64) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.backlogActive || s.backlog == nil {
		return nil, ErrBacklogEmpty
	}
	return s.backlog.Slice(reqOffset)
}

// SetMaster transitions this node to a replica of host:port.
func (s *State) SetMaster(host string, port int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.role = RoleSlave
	s.masterHost = host
	s.masterPort = port
	s.linkStatus = LinkStatusConnecting
	s.syncInProgress = false
	s.lastIO = s.clock.Now()
}

// PromoteToMaster transitions a replica back to master (REPLICAOF NO ONE).
func (s *State) PromoteToMaster() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.role = RoleMaster
	s.masterHost = ""
	s.masterPort = 0
	s.linkStatus = LinkStatusDown
	s.syncInProgress = false
}

// Role returns the current role (RoleMaster or RoleSlave).
func (s *State) Role() Role {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.role
}

// IsMaster returns true if the node is currently a replication master.
func (s *State) IsMaster() bool {
	return s.Role() == RoleMaster
}

// IsSlave returns true if the node is currently a replication slave/replica.
func (s *State) IsSlave() bool {
	return s.Role() == RoleSlave
}

// MasterAddr returns the configured master host and port for a replica.
func (s *State) MasterAddr() (string, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.masterHost, s.masterPort
}

// LinkStatus returns the replica's connection status to master.
func (s *State) LinkStatus() LinkStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.linkStatus
}

// SetLinkStatus updates the replica's master link status.
func (s *State) SetLinkStatus(status LinkStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.linkStatus = status
	s.lastIO = s.clock.Now()
}

// MasterReplID returns the primary 40-hex replication ID.
func (s *State) MasterReplID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.masterReplID
}

// SetMasterReplID updates the master replication ID.
func (s *State) SetMasterReplID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.masterReplID = id
}

// MasterReplOffset returns the current master replication stream offset.
func (s *State) MasterReplOffset() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.masterReplOffset
}

// SetMasterReplOffset sets the current master replication offset.
func (s *State) SetMasterReplOffset(offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.masterReplOffset = offset
}

// SlaveReplOffset returns the replica's processed stream offset.
func (s *State) SlaveReplOffset() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.slaveReplOffset
}

// SetSlaveReplOffset updates the replica's processed stream offset.
func (s *State) SetSlaveReplOffset(offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slaveReplOffset = offset
}

// SlaveReadOnly returns true if writes are rejected on replica.
func (s *State) SlaveReadOnly() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.slaveReadOnly
}

// ConnectedSlaves returns the count of active connected replicas.
func (s *State) ConnectedSlaves() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connectedSlaves
}

// SetConnectedSlaves updates connected replicas count.
func (s *State) SetConnectedSlaves(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connectedSlaves = count
}

// FormatInfo renders the # Replication section for INFO.
func (s *State) FormatInfo() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var b strings.Builder
	b.WriteString("# Replication\r\n")

	if s.role == RoleMaster {
		field(&b, "role", "master")
		field(&b, "connected_slaves", fmt.Sprintf("%d", s.connectedSlaves))
		field(&b, "master_failover_state", "no-failover")
		field(&b, "master_replid", s.masterReplID)
		field(&b, "master_replid2", s.masterReplID2)
		field(&b, "master_repl_offset", fmt.Sprintf("%d", s.masterReplOffset))
		field(&b, "second_repl_offset", fmt.Sprintf("%d", s.secondReplOffset))
		if s.backlogActive && s.backlog != nil {
			field(&b, "repl_backlog_active", "1")
			field(&b, "repl_backlog_size", fmt.Sprintf("%d", s.backlog.Size()))
			field(&b, "repl_backlog_first_byte_offset", fmt.Sprintf("%d", s.backlog.FirstByteOffset()))
			field(&b, "repl_backlog_histlen", fmt.Sprintf("%d", s.backlog.Histlen()))
		} else {
			field(&b, "repl_backlog_active", "0")
			field(&b, "repl_backlog_size", fmt.Sprintf("%d", s.backlogSize))
			field(&b, "repl_backlog_first_byte_offset", "0")
			field(&b, "repl_backlog_histlen", "0")
		}
	} else {
		field(&b, "role", "slave")
		field(&b, "master_host", s.masterHost)
		field(&b, "master_port", fmt.Sprintf("%d", s.masterPort))
		field(&b, "master_link_status", string(s.linkStatus))
		lastIO := -1
		if !s.lastIO.IsZero() {
			lastIO = int(s.clock.Since(s.lastIO).Seconds())
		}
		field(&b, "master_last_io_seconds_ago", fmt.Sprintf("%d", lastIO))
		syncInProgress := "0"
		if s.syncInProgress {
			syncInProgress = "1"
		}
		field(&b, "master_sync_in_progress", syncInProgress)
		field(&b, "slave_read_repl_offset", fmt.Sprintf("%d", s.slaveReplOffset))
		field(&b, "slave_repl_offset", fmt.Sprintf("%d", s.slaveReplOffset))
		field(&b, "master_link_down_since_seconds", "-1")
		field(&b, "slave_priority", fmt.Sprintf("%d", s.slavePriority))
		readOnly := "0"
		if s.slaveReadOnly {
			readOnly = "1"
		}
		field(&b, "slave_read_only", readOnly)
		field(&b, "replica_announced", "1")
		field(&b, "connected_slaves", fmt.Sprintf("%d", s.connectedSlaves))
		field(&b, "master_failover_state", "no-failover")
		field(&b, "master_replid", s.masterReplID)
		field(&b, "master_replid2", s.masterReplID2)
		field(&b, "master_repl_offset", fmt.Sprintf("%d", s.masterReplOffset))
		field(&b, "second_repl_offset", fmt.Sprintf("%d", s.secondReplOffset))
		field(&b, "repl_backlog_active", "0")
		field(&b, "repl_backlog_size", fmt.Sprintf("%d", s.backlogSize))
		field(&b, "repl_backlog_first_byte_offset", "0")
		field(&b, "repl_backlog_histlen", "0")
	}

	return b.String()
}

func field(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteByte(':')
	b.WriteString(value)
	b.WriteString("\r\n")
}
