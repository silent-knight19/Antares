package repl

import (
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestStateTransitions(t *testing.T) {
	clk := clock.NewFake(time.Unix(1000000, 0))
	s := NewState("c0867a5c1234567890abcdef1234567890abcdef", clk)

	if !s.IsMaster() || s.Role() != RoleMaster {
		t.Fatalf("initial role = %s, want master", s.Role())
	}
	if s.MasterReplID() != "c0867a5c1234567890abcdef1234567890abcdef" {
		t.Fatalf("replID = %s", s.MasterReplID())
	}

	info := s.FormatInfo()
	if !strings.Contains(info, "role:master") || !strings.Contains(info, "connected_slaves:0") {
		t.Fatalf("initial info = %s", info)
	}

	// Transition to replica
	s.SetMaster("127.0.0.1", 6379)
	if !s.IsSlave() || s.Role() != RoleSlave {
		t.Fatalf("role after SetMaster = %s, want slave", s.Role())
	}
	host, port := s.MasterAddr()
	if host != "127.0.0.1" || port != 6379 {
		t.Fatalf("master addr = %s:%d, want 127.0.0.1:6379", host, port)
	}
	if s.LinkStatus() != LinkStatusConnecting {
		t.Fatalf("link status = %s, want connecting", s.LinkStatus())
	}

	s.SetLinkStatus(LinkStatusUp)
	if s.LinkStatus() != LinkStatusUp {
		t.Fatalf("link status = %s, want up", s.LinkStatus())
	}

	slaveInfo := s.FormatInfo()
	if !strings.Contains(slaveInfo, "role:slave") ||
		!strings.Contains(slaveInfo, "master_host:127.0.0.1") ||
		!strings.Contains(slaveInfo, "master_port:6379") ||
		!strings.Contains(slaveInfo, "master_link_status:up") ||
		!strings.Contains(slaveInfo, "slave_read_only:1") {
		t.Fatalf("slave info = %s", slaveInfo)
	}

	// Promote back to master (REPLICAOF NO ONE)
	s.PromoteToMaster()
	if !s.IsMaster() {
		t.Fatalf("role after promote = %s, want master", s.Role())
	}
	masterInfo := s.FormatInfo()
	if !strings.Contains(masterInfo, "role:master") {
		t.Fatalf("promoted info = %s", masterInfo)
	}
}

func TestOffsetsAndBacklog(t *testing.T) {
	s := NewState("abcde", nil)
	if s.MasterReplOffset() != 0 {
		t.Fatalf("offset = %d, want 0", s.MasterReplOffset())
	}

	s.FeedBacklog(make([]byte, 150))
	if s.MasterReplOffset() != 150 {
		t.Fatalf("offset = %d, want 150", s.MasterReplOffset())
	}

	s.SetSlaveReplOffset(120)
	if s.SlaveReplOffset() != 120 {
		t.Fatalf("slave offset = %d, want 120", s.SlaveReplOffset())
	}
}

func TestFeedBacklogSyncBuffers(t *testing.T) {
	s := NewState("testreplid", nil)
	buf1 := NewSyncBuffer(1024)
	buf2 := NewSyncBuffer(1024)

	s.RegisterSyncBuffer(buf1)
	s.RegisterSyncBuffer(buf2)
	if s.ActiveSyncBuffers() != 2 {
		t.Fatalf("active sync buffers = %d, want 2", s.ActiveSyncBuffers())
	}

	cmd := []byte("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n")
	s.FeedBacklog(cmd)

	if buf1.Buffered() != int64(len(cmd)) || buf2.Buffered() != int64(len(cmd)) {
		t.Fatalf("buffers not updated: buf1=%d, buf2=%d, want %d", buf1.Buffered(), buf2.Buffered(), len(cmd))
	}

	s.UnregisterSyncBuffer(buf1)
	if s.ActiveSyncBuffers() != 1 {
		t.Fatalf("active sync buffers after unregister = %d, want 1", s.ActiveSyncBuffers())
	}

	cmd2 := []byte("*2\r\n$3\r\nDEL\r\n$1\r\nk\r\n")
	s.FeedBacklog(cmd2)

	if buf1.Buffered() != int64(len(cmd)) {
		t.Fatalf("buf1 received mutation after unregister: got %d, want %d", buf1.Buffered(), len(cmd))
	}
	if buf2.Buffered() != int64(len(cmd)+len(cmd2)) {
		t.Fatalf("buf2 missing second mutation: got %d, want %d", buf2.Buffered(), len(cmd)+len(cmd2))
	}
}

func TestSlaveManagementAndInfoFormatting(t *testing.T) {
	clk := clock.NewFake(time.Unix(1000000, 0))
	s := NewState("abcde12345", clk)

	if s.ConnectedSlaves() != 0 {
		t.Fatalf("connected slaves = %d, want 0", s.ConnectedSlaves())
	}

	// Register slave 0
	sID1 := s.AddSlave("127.0.0.1", 6381)
	if sID1 == 0 {
		t.Fatalf("slave 1 ID = 0, want non-zero")
	}
	if s.ConnectedSlaves() != 1 {
		t.Fatalf("connected slaves = %d, want 1", s.ConnectedSlaves())
	}

	info := s.FormatInfo()
	wantSlave0 := "slave0:ip=127.0.0.1,port=6381,state=online,offset=0,lag=0"
	if !strings.Contains(info, "connected_slaves:1") || !strings.Contains(info, wantSlave0) {
		t.Fatalf("INFO replication missing slave0: \n%s", info)
	}

	// Advance clock by 4 seconds
	clk.Advance(4 * time.Second)
	infoLag := s.FormatInfo()
	wantLag4 := "slave0:ip=127.0.0.1,port=6381,state=online,offset=0,lag=4"
	if !strings.Contains(infoLag, wantLag4) {
		t.Fatalf("INFO replication missing lag=4:\n%s", infoLag)
	}

	// Slave acknowledges offset 250
	s.UpdateSlaveAck(sID1, 250)
	infoAck := s.FormatInfo()
	wantAck := "slave0:ip=127.0.0.1,port=6381,state=online,offset=250,lag=0"
	if !strings.Contains(infoAck, wantAck) {
		t.Fatalf("INFO replication missing offset=250,lag=0:\n%s", infoAck)
	}

	// Add slave 2
	sID2 := s.AddSlave("192.168.1.50", 6382)
	s.UpdateSlaveAck(sID2, 100)
	clk.Advance(2 * time.Second)

	info2 := s.FormatInfo()
	if !strings.Contains(info2, "connected_slaves:2") ||
		!strings.Contains(info2, "slave0:ip=127.0.0.1,port=6381,state=online,offset=250,lag=2") ||
		!strings.Contains(info2, "slave1:ip=192.168.1.50,port=6382,state=online,offset=100,lag=2") {
		t.Fatalf("INFO replication missing both slaves:\n%s", info2)
	}

	// Remove slave 1, verify remaining slave re-indexed as slave0
	s.RemoveSlave(sID1)
	if s.ConnectedSlaves() != 1 {
		t.Fatalf("connected slaves after remove = %d, want 1", s.ConnectedSlaves())
	}
	infoAfterRemove := s.FormatInfo()
	if strings.Contains(infoAfterRemove, "slave1:") {
		t.Fatalf("INFO replication still contains slave1 after remove:\n%s", infoAfterRemove)
	}
	if !strings.Contains(infoAfterRemove, "slave0:ip=192.168.1.50,port=6382,state=online,offset=100,lag=2") {
		t.Fatalf("INFO replication missing re-indexed slave0:\n%s", infoAfterRemove)
	}
}

func TestPromotionShiftsReplicationHistory(t *testing.T) {
	s := NewState("masterA", nil)
	s.SetMaster("127.0.0.1", 6379)
	s.SetMasterReplID("masterA")
	s.SetMasterReplOffset(42)
	s.SetSlaveReplOffset(42)

	s.PromoteToMaster()

	if s.MasterReplID() == "masterA" {
		t.Fatal("promotion kept the old master replid; split-brain history was not shifted")
	}
	if s.masterReplID2 != "masterA" {
		t.Fatalf("master_replid2 = %q, want masterA", s.masterReplID2)
	}
	if s.secondReplOffset != 43 {
		t.Fatalf("second_repl_offset = %d, want 43", s.secondReplOffset)
	}
	if id, off := s.PSyncArgs(); id != s.MasterReplID() || off != 0 {
		t.Fatalf("PSyncArgs after promotion = %q %d, want new replid and offset 0", id, off)
	}

	before := s.MasterReplID()
	s.PromoteToMaster()
	if s.MasterReplID() != before {
		t.Fatal("promotion of an existing master changed its replid")
	}
}

func TestFeedReplicationPingOnlyWithReplicas(t *testing.T) {
	s := NewState("pingtest", nil)
	if s.FeedReplicationPing() {
		t.Fatal("ping fed with no replicas")
	}
	if s.MasterReplOffset() != 0 {
		t.Fatalf("offset = %d, want 0", s.MasterReplOffset())
	}

	s.AddSlave("127.0.0.1", 6380)
	if !s.FeedReplicationPing() {
		t.Fatal("ping not fed with a connected replica")
	}
	if s.MasterReplOffset() != int64(len(replicationPing)) {
		t.Fatalf("offset = %d, want %d", s.MasterReplOffset(), len(replicationPing))
	}
}

func TestPromoteEmptyHostSlave(t *testing.T) {
	s := NewState("emptyhost", nil)
	s.SetMaster("", 6379)
	s.PromoteToMaster()
	if !s.IsMaster() {
		t.Fatalf("role = %s, want master", s.Role())
	}
}

func TestStaleSyncCannotOverwritePromotion(t *testing.T) {
	s := NewState("oldmaster", nil)
	s.SetMaster("127.0.0.1", 6379)
	gen := s.BeginSync()
	s.PromoteToMaster()
	promotedID := s.MasterReplID()

	if s.BeginFullResync(gen) {
		t.Fatal("stale full-resync invalidation was accepted")
	}
	if s.SetMasterReplIDForSync("oldmaster", gen) {
		t.Fatal("stale replid write was accepted")
	}
	if s.SetSlaveReplOffsetForSync(99, gen) {
		t.Fatal("stale offset write was accepted")
	}
	if s.SetLinkStatusForSync(LinkStatusUp, gen) {
		t.Fatal("stale link-status write was accepted")
	}
	if s.MasterReplID() != promotedID {
		t.Fatalf("replid = %q, want promoted %q", s.MasterReplID(), promotedID)
	}
	if s.SlaveReplOffset() != 0 {
		t.Fatalf("slave offset = %d, want 0", s.SlaveReplOffset())
	}
	if s.LinkStatus() != LinkStatusDown {
		t.Fatalf("link status = %s, want down", s.LinkStatus())
	}
}

func TestRegisterSyncBufferCapturesOffsetAtomically(t *testing.T) {
	s := NewState("atomic", nil)
	buf := NewSyncBuffer(1024)
	off := s.RegisterSyncBufferWithOffset(buf)
	if off != 0 {
		t.Fatalf("offset = %d, want 0", off)
	}
	if !s.FeedReplicationPing() {
		t.Fatal("ping not fed to registered buffer")
	}
	if buf.Buffered() != int64(len(replicationPing)) {
		t.Fatalf("buffered = %d, want %d", buf.Buffered(), len(replicationPing))
	}
	if s.MasterReplOffset() != off+int64(len(replicationPing)) {
		t.Fatalf("offset = %d, want %d", s.MasterReplOffset(), off+int64(len(replicationPing)))
	}
}
