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
