package server

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/silent-knight19/antares/internal/protocol"
)

const redisVersion = "7.4.11"

var (
	infoSectionOrder = []string{
		"server", "clients", "memory", "persistence", "stats", "replication",
		"cpu", "module_list", "commandstats", "errorstats", "latencystats",
		"cluster", "keyspace",
	}
	defaultInfoSections = map[string]bool{
		"server": true, "clients": true, "memory": true, "persistence": true,
		"stats": true, "replication": true, "cpu": true, "module_list": true,
		"errorstats": true, "cluster": true, "keyspace": true,
	}
)

func registerInfoCommands(reg *Registry, e *Executor) {
	reg.Register(Command{Name: "info", Arity: -1, Handler: func(args [][]byte, out *protocol.Encoder) {
		infoCommand(e, args, out)
	}})
}

func infoCommand(e *Executor, args [][]byte, out *protocol.Encoder) {
	sections := resolveInfoSections(args)
	var b strings.Builder
	for i, section := range sections {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(e.infoSection(section))
	}
	out.Bulk([]byte(b.String()))
}

func resolveInfoSections(args [][]byte) []string {
	if len(args) == 0 {
		return orderedSections(defaultInfoSections)
	}
	selected := make(map[string]bool)
	all := false
	for _, arg := range args {
		switch strings.ToLower(string(arg)) {
		case "default":
			for name := range defaultInfoSections {
				selected[name] = true
			}
		case "all", "everything":
			all = true
		case "modules":
			selected["module_list"] = true
		default:
			selected[strings.ToLower(string(arg))] = true
		}
	}
	if all {
		return infoSectionOrder
	}
	return orderedSections(selected)
}

func orderedSections(selected map[string]bool) []string {
	sections := make([]string, 0, len(selected))
	for _, name := range infoSectionOrder {
		if selected[name] {
			sections = append(sections, name)
		}
	}
	return sections
}

func (e *Executor) infoSection(name string) string {
	var b strings.Builder
	switch name {
	case "server":
		e.serverInfo(&b)
	case "clients":
		e.clientsInfo(&b)
	case "memory":
		e.memoryInfo(&b)
	case "persistence":
		e.persistenceInfo(&b)
	case "stats":
		e.statsInfo(&b)
	case "replication":
		e.replicationInfo(&b)
	case "cpu":
		b.WriteString("# CPU\r\n")
		for _, field := range []string{"used_cpu_sys", "used_cpu_user", "used_cpu_sys_children", "used_cpu_user_children", "used_cpu_sys_main_thread", "used_cpu_user_main_thread"} {
			fmt.Fprintf(&b, "%s:0.000000\r\n", field)
		}
	case "module_list":
		b.WriteString("# Modules\r\n")
	case "commandstats":
		b.WriteString("# Commandstats\r\n")
	case "errorstats":
		b.WriteString("# Errorstats\r\n")
	case "latencystats":
		b.WriteString("# Latencystats\r\n")
	case "cluster":
		b.WriteString("# Cluster\r\ncluster_enabled:0\r\n")
	case "keyspace":
		e.keyspaceInfo(&b)
	}
	return b.String()
}

func (e *Executor) serverInfo(b *strings.Builder) {
	now := e.clock.Now()
	uptime := int64(now.Sub(e.start) / time.Second)
	if uptime < 0 {
		uptime = 0
	}
	port := 0
	bind := ""
	if addr := e.stats.Addr(); addr != "" {
		if host, p, err := net.SplitHostPort(addr); err == nil {
			bind = host
			port, _ = strconv.Atoi(p)
		}
	}
	executable, _ := os.Executable()

	b.WriteString("# Server\r\n")
	field(b, "redis_version", redisVersion)
	field(b, "redis_git_sha1", "00000000")
	field(b, "redis_git_dirty", "0")
	field(b, "redis_build_id", "antares")
	field(b, "redis_mode", "standalone")
	field(b, "os", runtime.GOOS+" "+runtime.GOARCH)
	field(b, "arch_bits", strconv.Itoa(strconv.IntSize))
	field(b, "monotonic_clock", "go")
	field(b, "multiplexing_api", "go-net")
	field(b, "atomicvar_api", "go-atomic")
	field(b, "process_id", strconv.Itoa(os.Getpid()))
	field(b, "process_supervised", "no")
	field(b, "run_id", e.stats.RunID)
	field(b, "tcp_port", strconv.Itoa(port))
	field(b, "server_time_usec", strconv.FormatInt(now.UnixMicro(), 10))
	field(b, "uptime_in_seconds", strconv.FormatInt(uptime, 10))
	field(b, "uptime_in_days", strconv.FormatInt(uptime/86400, 10))
	field(b, "hz", strconv.Itoa(e.hz))
	field(b, "configured_hz", strconv.Itoa(e.hz))
	field(b, "lru_clock", "0")
	field(b, "executable", executable)
	field(b, "config_file", "")
	field(b, "io_threads_active", "0")
	if bind != "" {
		field(b, "listener0", fmt.Sprintf("name=tcp,bind=%s,port=%d", bind, port))
	}
}

func (e *Executor) clientsInfo(b *strings.Builder) {
	b.WriteString("# Clients\r\n")
	field(b, "connected_clients", strconv.FormatInt(e.stats.Connected.Load(), 10))
	field(b, "cluster_connections", "0")
	field(b, "maxclients", strconv.FormatInt(e.stats.MaxClients.Load(), 10))
	field(b, "client_recent_max_input_buffer", "0")
	field(b, "client_recent_max_output_buffer", "0")
	field(b, "blocked_clients", "0")
	field(b, "tracking_clients", "0")
	field(b, "pubsub_clients", "0")
	field(b, "watching_clients", "0")
	field(b, "clients_in_timeout_table", "0")
	field(b, "total_watched_keys", "0")
	field(b, "total_blocking_keys", "0")
	field(b, "total_blocking_keys_on_nokey", "0")
}

func (e *Executor) memoryInfo(b *strings.Builder) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	usage := e.store.MemoryUsage()
	used := uint64(usage.Total)
	overhead := uint64(usage.Total - usage.Dataset)
	dataset := uint64(usage.Dataset)
	rss := m.Sys
	e.stats.ObserveMemory(used)
	peak := e.stats.PeakMemory.Load()
	if peak < used {
		peak = used
	}
	peakPerc := 100.0
	if peak > 0 {
		peakPerc = float64(used) / float64(peak) * 100
	}
	datasetPerc := 100.0
	if used > 0 {
		datasetPerc = float64(dataset) / float64(used) * 100
	}
	fragRatio := 0.0
	if used > 0 {
		fragRatio = float64(rss) / float64(used)
	}

	b.WriteString("# Memory\r\n")
	field(b, "used_memory", strconv.FormatUint(used, 10))
	field(b, "used_memory_human", bytesHuman(used))
	field(b, "used_memory_rss", strconv.FormatUint(rss, 10))
	field(b, "used_memory_rss_human", bytesHuman(rss))
	field(b, "used_memory_peak", strconv.FormatUint(peak, 10))
	field(b, "used_memory_peak_human", bytesHuman(peak))
	fmt.Fprintf(b, "used_memory_peak_perc:%.2f%%\r\n", peakPerc)
	field(b, "used_memory_overhead", strconv.FormatUint(overhead, 10))
	field(b, "used_memory_startup", "0")
	field(b, "used_memory_dataset", strconv.FormatUint(dataset, 10))
	fmt.Fprintf(b, "used_memory_dataset_perc:%.2f%%\r\n", datasetPerc)
	field(b, "allocator_allocated", strconv.FormatUint(m.Alloc, 10))
	field(b, "allocator_active", strconv.FormatUint(m.HeapInuse, 10))
	field(b, "allocator_resident", strconv.FormatUint(rss, 10))
	field(b, "total_system_memory", "0")
	field(b, "total_system_memory_human", "0B")
	field(b, "used_memory_lua", "0")
	field(b, "used_memory_vm_eval", "0")
	field(b, "used_memory_scripts_eval", "0")
	field(b, "number_of_cached_scripts", "0")
	field(b, "number_of_functions", "0")
	field(b, "number_of_libraries", "0")
	field(b, "used_memory_vm_functions", "0")
	field(b, "used_memory_vm_total", "0")
	field(b, "used_memory_functions", "0")
	field(b, "used_memory_scripts", "0")
	field(b, "maxmemory", strconv.FormatInt(e.maxmemory, 10))
	field(b, "maxmemory_human", bytesHuman(uint64(e.maxmemory)))
	field(b, "maxmemory_policy", e.maxmemoryPolicy)
	fmt.Fprintf(b, "mem_fragmentation_ratio:%.2f\r\n", fragRatio)
	field(b, "mem_allocator", "go")
}

func (e *Executor) persistenceInfo(b *strings.Builder) {
	aofEnabled := 0
	aofRewriting := 0
	rewrites := uint64(0)
	currentSize := int64(0)
	baseSize := int64(0)
	lastStatus := "ok"
	if stats, ok := e.AOFStats(); ok {
		aofEnabled = 1
		rewrites = stats.Rewrites
		currentSize = stats.CurrentSize
		baseSize = stats.BaseSize
		if !stats.LastWriteOK {
			lastStatus = "err"
		}
	}
	if e.Rewriting() {
		aofRewriting = 1
	}

	b.WriteString("# Persistence\r\n")
	for _, kv := range [][2]string{
		{"loading", "0"},
		{"async_loading", "0"},
		{"rdb_changes_since_last_save", "0"},
		{"rdb_bgsave_in_progress", "0"},
		{"rdb_last_save_time", "0"},
		{"rdb_last_bgsave_status", "ok"},
		{"rdb_last_bgsave_time_sec", "-1"},
		{"rdb_current_bgsave_time_sec", "-1"},
		{"rdb_saves", "0"},
		{"aof_enabled", strconv.Itoa(aofEnabled)},
		{"aof_rewrite_in_progress", strconv.Itoa(aofRewriting)},
		{"aof_rewrite_scheduled", "0"},
		{"aof_last_rewrite_time_sec", "-1"},
		{"aof_current_rewrite_time_sec", "-1"},
		{"aof_last_bgrewrite_status", "ok"},
		{"aof_rewrites", strconv.FormatUint(rewrites, 10)},
		{"aof_current_size", strconv.FormatInt(currentSize, 10)},
		{"aof_base_size", strconv.FormatInt(baseSize, 10)},
		{"aof_last_write_status", lastStatus},
	} {
		field(b, kv[0], kv[1])
	}
}

func (e *Executor) statsInfo(b *strings.Builder) {
	b.WriteString("# Stats\r\n")
	field(b, "total_connections_received", strconv.FormatUint(e.stats.Connections.Load(), 10))
	field(b, "total_commands_processed", strconv.FormatUint(e.stats.Commands.Load(), 10))
	field(b, "instantaneous_ops_per_sec", "0")
	field(b, "total_net_input_bytes", strconv.FormatUint(e.stats.NetInput.Load(), 10))
	field(b, "total_net_output_bytes", strconv.FormatUint(e.stats.NetOut.Load(), 10))
	field(b, "instantaneous_input_kbps", "0.00")
	field(b, "instantaneous_output_kbps", "0.00")
	for _, name := range []string{
		"rejected_connections", "sync_full", "sync_partial_ok", "sync_partial_err",
		"expired_stale_perc", "expired_time_cap_reached_count",
		"expire_cycle_cpu_milliseconds", "keyspace_hits",
		"keyspace_misses", "pubsub_channels", "pubsub_patterns", "latest_fork_usec",
		"total_forks", "total_reads_processed", "total_writes_processed",
		"total_error_replies",
	} {
		field(b, name, "0")
	}
	field(b, "evicted_keys", strconv.FormatUint(e.stats.EvictedKeys.Load(), 10))
	field(b, "expired_keys", strconv.FormatUint(e.store.ExpiredCount(), 10))
}

func (e *Executor) replicationInfo(b *strings.Builder) {
	if e.repl != nil {
		b.WriteString(e.repl.FormatInfo())
		return
	}
	b.WriteString("# Replication\r\n")
	field(b, "role", "master")
	field(b, "connected_slaves", "0")
	field(b, "master_failover_state", "no-failover")
	field(b, "master_replid", e.stats.ReplID)
	field(b, "master_replid2", strings.Repeat("0", 40))
	field(b, "master_repl_offset", "0")
	field(b, "second_repl_offset", "-1")
	field(b, "repl_backlog_active", "0")
	field(b, "repl_backlog_size", "1048576")
	field(b, "repl_backlog_first_byte_offset", "0")
	field(b, "repl_backlog_histlen", "0")
}

func (e *Executor) keyspaceInfo(b *strings.Builder) {
	b.WriteString("# Keyspace\r\n")
	if keys := e.store.Len(); keys > 0 {
		fmt.Fprintf(b, "db0:keys=%d,expires=%d,avg_ttl=0,subexpiry=0\r\n", keys, e.store.VolatileLen())
	}
}

func field(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteByte(':')
	b.WriteString(value)
	b.WriteString("\r\n")
}

func bytesHuman(n uint64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatUint(n, 10) + "B"
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f%c", float64(n)/float64(div), "KMGTPE"[exp])
}
