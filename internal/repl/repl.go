// Package repl implements REPLICAOF, PSYNC full/partial resync, the backlog
// ring buffer, and the replica apply loop (ADR-007, ADR-010).
package repl

import (
	"crypto/rand"
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

// replicationPing is the keepalive command masters inject into the replication
// stream so replicas can detect a silent link.
var replicationPing = []byte("*1\r\n$4\r\nPING\r\n")

// State tracks replication role, master/replica offsets, and link state.
type State struct {
	mu sync.RWMutex

	role       Role
	masterHost string
	masterPort int
	linkStatus LinkStatus

	masterReplID     string
	masterReplID2    string
	masterReplIDSet  bool
	masterReplOffset int64
	secondReplOffset int64

	slaveReplOffset int64
	slaveReadOnly   bool
	slavePriority   int

	slaves          []*SlaveInfo
	slavesByID      map[uint64]*SlaveInfo
	nextSlaveID     uint64
	lastIO          time.Time
	syncInProgress  bool
	syncEpoch       uint64

	backlogActive   bool
	backlogSize     int64
	backlog         *Backlog
	syncBuffers     map[*SyncBuffer]struct{}
	syncBufferLimit int64

	clock clock.Clock
}

// SlaveInfo tracks a connected replica's registration, offset, and lag (ADR-007, PLAN §5 S1.5).
type SlaveInfo struct {
	ID      uint64
	IP      string
	Port    int
	State   string
	Offset  int64
	LastAck time.Time
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
		slavesByID:       make(map[uint64]*SlaveInfo),
		backlogActive:    true,
		backlogSize:      DefaultBacklogSize,
		backlog:          b,
		syncBuffers:      make(map[*SyncBuffer]struct{}),
		syncBufferLimit:  DefaultSyncBufferCap,
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
// master stream offset, updating backlog history, and feeding all active per-replica
// concurrent write buffers (ADR-010).
func (s *State) FeedBacklog(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feedBacklogLocked(p)
}

// FeedReplicationPing appends a keepalive PING when at least one replica is
// connected or syncing. It returns false when there was nothing to feed.
func (s *State) FeedReplicationPing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.slaves) == 0 && len(s.syncBuffers) == 0 {
		return false
	}
	s.feedBacklogLocked(replicationPing)
	return true
}

func (s *State) feedBacklogLocked(p []byte) {
	if s.backlog == nil {
		s.backlog = NewBacklog(s.backlogSize)
		s.backlogActive = true
	}
	_, _ = s.backlog.Write(p)
	s.masterReplOffset = s.backlog.Offset()

	for buf := range s.syncBuffers {
		_ = buf.Append(p)
	}
}

// RegisterSyncBuffer registers a per-replica concurrent write buffer to receive mutations (ADR-010).
func (s *State) RegisterSyncBuffer(buf *SyncBuffer) {
	if buf == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registerSyncBufferLocked(buf)
}

// RegisterSyncBufferWithOffset registers buf and returns the master offset under
// the same lock. A keepalive PING cannot slip between the offset capture and
// buffer registration and leave the replica permanently behind.
func (s *State) RegisterSyncBufferWithOffset(buf *SyncBuffer) int64 {
	if buf == nil {
		return s.MasterReplOffset()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registerSyncBufferLocked(buf)
	return s.masterReplOffset
}

func (s *State) registerSyncBufferLocked(buf *SyncBuffer) {
	if s.syncBuffers == nil {
		s.syncBuffers = make(map[*SyncBuffer]struct{})
	}
	s.syncBuffers[buf] = struct{}{}
}

// UnregisterSyncBuffer removes a per-replica concurrent write buffer once snapshot streaming completes.
func (s *State) UnregisterSyncBuffer(buf *SyncBuffer) {
	if buf == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.syncBuffers, buf)
}

// SetSyncBufferLimit caps each replica's pending output buffer. A limit <= 0
// restores DefaultSyncBufferCap. The cap bounds master memory; exceeding it
// disconnects the slow replica instead of blocking the executor (S2.3).
func (s *State) SetSyncBufferLimit(limit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = DefaultSyncBufferCap
	}
	s.syncBufferLimit = limit
}

// SyncBufferLimit returns the per-replica output buffer cap in bytes.
func (s *State) SyncBufferLimit() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.syncBufferLimit
}

// ActiveSyncBuffers reports the count of currently registered sync buffers.
func (s *State) ActiveSyncBuffers() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.syncBuffers)
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

// CanPartialResyncWithID decides PSYNC between +CONTINUE and +FULLRESYNC:
// the requested replication ID must match ours and the offset must fall inside
// the backlog window (S2.1).
func (s *State) CanPartialResyncWithID(reqReplID string, reqOffset int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.canPartialResyncLocked(reqReplID, reqOffset)
}

func (s *State) canPartialResyncLocked(reqReplID string, reqOffset int64) bool {
	if !s.backlogActive || s.backlog == nil {
		return false
	}
	if reqReplID == "" || reqReplID == "?" || reqReplID != s.masterReplID {
		return false
	}
	return s.backlog.CanPartialResync(reqOffset)
}

// StartPartialResync atomically captures the backlog tail after reqOffset and
// registers a live write buffer, so no mutation can slip between the catch-up
// slice and the ongoing stream (S2.2). The caller owns the returned buffer and
// must unregister and close it when the link ends.
func (s *State) StartPartialResync(reqReplID string, reqOffset int64) ([]byte, *SyncBuffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canPartialResyncLocked(reqReplID, reqOffset) {
		return nil, nil, false
	}
	backlog, err := s.backlog.Slice(reqOffset)
	if err != nil {
		return nil, nil, false
	}
	buf := NewSyncBuffer(s.syncBufferLimit)
	if s.syncBuffers == nil {
		s.syncBuffers = make(map[*SyncBuffer]struct{})
	}
	s.syncBuffers[buf] = struct{}{}
	return backlog, buf, true
}

// PSyncArgs returns the replication ID and offset a replica should send on its
// next PSYNC. Before the first successful sync it asks for a full resync.
func (s *State) PSyncArgs() (string, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.masterReplIDSet {
		return "?", -1
	}
	return s.masterReplID, s.slaveReplOffset
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
	s.syncEpoch++
	s.lastIO = s.clock.Now()
}

// PromoteToMaster transitions a replica back to master (REPLICAOF NO ONE).
// Promotion starts a new replication history: the old replid is kept as replid2
// and a fresh replid is generated, so the old master cannot answer +CONTINUE
// from a history that has diverged (split-brain safety, S2.4).
func (s *State) PromoteToMaster() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.role == RoleMaster {
		return
	}

	s.syncEpoch++
	s.masterReplID2 = s.masterReplID
	s.secondReplOffset = s.slaveReplOffset + 1
	s.masterReplID = newReplID()
	s.masterReplIDSet = true

	// New history starts at zero so offsets, snapshots, and the backlog agree.
	s.backlog = NewBacklog(s.backlogSize)
	s.masterReplOffset = 0
	s.slaveReplOffset = 0

	s.role = RoleMaster
	s.masterHost = ""
	s.masterPort = 0
	s.linkStatus = LinkStatusDown
	s.syncInProgress = false
}

func newReplID() string {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strings.Repeat("0", 40)
	}
	return fmt.Sprintf("%x", b[:])
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

// BeginSync starts a new replica sync attempt and returns its epoch. State
// writes from an older epoch are rejected so a canceled or superseded loop
// cannot overwrite a newer sync or a promotion.
func (s *State) BeginSync() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncEpoch++
	return s.syncEpoch
}

// SyncGeneration returns the current replica sync epoch.
func (s *State) SyncGeneration() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.syncEpoch
}

// BeginFullResync invalidates the current resume point for epoch gen. The next
// PSYNC asks for a full resync even if the snapshot stream then fails.
func (s *State) BeginFullResync(gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncEpoch != gen {
		return false
	}
	s.masterReplIDSet = false
	s.slaveReplOffset = 0
	return true
}

// SetMasterReplIDForSync records a master replid only for the current epoch.
func (s *State) SetMasterReplIDForSync(id string, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncEpoch != gen {
		return false
	}
	s.masterReplID = id
	s.masterReplIDSet = true
	return true
}

// SetSlaveReplOffsetForSync records a processed offset only for the current epoch.
func (s *State) SetSlaveReplOffsetForSync(offset int64, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncEpoch != gen {
		return false
	}
	s.slaveReplOffset = offset
	return true
}

// SetLinkStatusForSync updates link status only for the current epoch.
func (s *State) SetLinkStatusForSync(status LinkStatus, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncEpoch != gen {
		return false
	}
	s.linkStatus = status
	s.lastIO = s.clock.Now()
	return true
}

// MasterReplID returns the primary 40-hex replication ID.
func (s *State) MasterReplID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.masterReplID
}

// SetMasterReplID records the master replication ID learned during sync.
func (s *State) SetMasterReplID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.masterReplID = id
	s.masterReplIDSet = true
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

// SetSlaveReadOnly configures whether the replica rejects client writes.
func (s *State) SetSlaveReadOnly(ro bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slaveReadOnly = ro
}

// AddSlave registers a replica on the master node and returns its allocated ID (S1.5).
func (s *State) AddSlave(ip string, port int) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ip == "" {
		ip = "127.0.0.1"
	}
	s.nextSlaveID++
	sl := &SlaveInfo{
		ID:      s.nextSlaveID,
		IP:      ip,
		Port:    port,
		State:   "online",
		Offset:  0,
		LastAck: s.clock.Now(),
	}
	if s.slavesByID == nil {
		s.slavesByID = make(map[uint64]*SlaveInfo)
	}
	s.slavesByID[sl.ID] = sl
	s.slaves = append(s.slaves, sl)
	return sl.ID
}

// RemoveSlave removes a replica when its link closes (S1.5).
func (s *State) RemoveSlave(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.slavesByID, id)
	for i, sl := range s.slaves {
		if sl.ID == id {
			copy(s.slaves[i:], s.slaves[i+1:])
			s.slaves[len(s.slaves)-1] = nil
			s.slaves = s.slaves[:len(s.slaves)-1]
			break
		}
	}
}

// UpdateSlavePort updates a replica's reported listening port (S1.5).
func (s *State) UpdateSlavePort(id uint64, port int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sl, ok := s.slavesByID[id]; ok {
		sl.Port = port
	}
}

// UpdateSlaveIP updates a replica's reported IP address (S1.5).
func (s *State) UpdateSlaveIP(id uint64, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sl, ok := s.slavesByID[id]; ok {
		sl.IP = ip
	}
}

// UpdateSlaveState updates a replica's replication state (S1.5).
func (s *State) UpdateSlaveState(id uint64, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sl, ok := s.slavesByID[id]; ok {
		sl.State = state
	}
}

// UpdateSlaveAck updates a replica's acknowledged offset and refreshes its last ACK timestamp (S1.5).
func (s *State) UpdateSlaveAck(id uint64, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sl, ok := s.slavesByID[id]; ok {
		sl.Offset = offset
		sl.LastAck = s.clock.Now()
	}
}

// GetSlave returns a copy of the specified replica's info.
func (s *State) GetSlave(id uint64) (SlaveInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sl, ok := s.slavesByID[id]
	if !ok {
		return SlaveInfo{}, false
	}
	return *sl, true
}

// Slaves returns a copy of all registered replicas.
func (s *State) Slaves() []SlaveInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]SlaveInfo, len(s.slaves))
	for i, sl := range s.slaves {
		res[i] = *sl
	}
	return res
}

// ConnectedSlaves returns the count of active connected replicas.
func (s *State) ConnectedSlaves() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.slaves)
}

// FormatInfo renders the # Replication section for INFO.
func (s *State) FormatInfo() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var b strings.Builder
	b.WriteString("# Replication\r\n")

	if s.role == RoleMaster {
		field(&b, "role", "master")
		field(&b, "connected_slaves", fmt.Sprintf("%d", len(s.slaves)))

		for i, sl := range s.slaves {
			lag := 0
			if !sl.LastAck.IsZero() {
				lag = int(s.clock.Since(sl.LastAck).Seconds())
				if lag < 0 {
					lag = 0
				}
			}
			state := sl.State
			if state == "" {
				state = "online"
			}
			ip := sl.IP
			if ip == "" {
				ip = "127.0.0.1"
			}
			field(&b, fmt.Sprintf("slave%d", i), fmt.Sprintf("ip=%s,port=%d,state=%s,offset=%d,lag=%d", ip, sl.Port, state, sl.Offset, lag))
		}

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
		field(&b, "connected_slaves", "0")
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
