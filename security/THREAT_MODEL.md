# Threat Model — Antares & Observatory

## 1. System Boundary and Assumptions

Antares is an in-memory, single-executor data store speaking Redis Serialization Protocol version 2 (RESP2), backed by a framed Append-Only File (AOF) with little-endian length/CRC32 framing, and asynchronous PSYNC replication. Observatory is an optional HTTP bridge and browser SPA providing local diagnostics, REST command execution, and performance telemetry.

### Trust Assumptions
1. **Host Environment:** Antares assumes it executes in a secure, non-hostile host OS environment. The local filesystem where the AOF, event logs, and temporary rewrite snapshots are written is assumed to be accessible only by the process user UID (`10001` in Docker).
2. **Network Perimeter:** Antares operates without authentication, access control lists (ACLs), or transport-layer encryption (TLS). By default, Antares binds to `127.0.0.1:6380` and Observatory binds to `127.0.0.1:6390`.
3. **Co-located Operator:** Observatory is designed as a developer console. It assumes all requests originate from the local browser or trusted management loopback network.

---

## 2. Invariants Enforced by the System

The architecture guarantees several key security and correctness invariants:
- **Keyspace Ownership (ADR-002):** A single executor goroutine exclusively owns the keyspace map and expiry dictionary. Connection goroutines only parse requests and encode responses; they never touch keyspace memory directly. This eliminates keyspace data races by construction without fine-grained mutexes.
- **Backpressure Over Rejection (ADR-002):** The executor request queue is bounded (1024). When saturated, client connection goroutines block on sending to the channel, translating load directly into TCP socket backpressure.
- **Atomic Transaction Turn (ADR-008/028):** `EXEC` runs the entire transaction queue within a single executor turn. AOF mutations for all commands in a transaction are written into a single frame, guaranteeing atomic replay.
- **Persistence-First Write Contract (ADR-004/030):** In-memory mutation is followed by AOF frame append before the replication backlog is fed. A write failing AOF persistence enters sticky `MISCONF` mode and never propagates to replicas.
- **Framed Replay Integrity (ADR-004):** Every AOF record is framed with `[u32 length][u32 IEEE-CRC32][RESP payload]`. Recovery validates checksums and declared lengths against file boundaries, automatically truncating incomplete torn writes at EOF and refusing corrupted mid-log frames unless `-aof-repair` is explicitly commanded.
- **Bounded Memory Bounds (ADR-026/027):** Memory limits are tracked via a logical memory model (`store.MemoryUsage()`), and approximated LRU eviction (sample 5, pool 16) frees victims before rejecting memory-growing writes with `-OOM`.

---

## 3. Explicit Non-Goals (Out of Scope)

The following capabilities are documented non-goals. Their absence is an architectural choice, not a defect:
1. **Authentication & Authorization (No AUTH / ACL):** Antares does not verify client passwords or enforce role-based access control. Any client that establishes a TCP connection possesses full administrative privileges (including `FLUSHALL`, `CONFIG`, and `BGREWRITEAOF`).
2. **Network Encryption (No TLS):** Wire traffic between clients, servers, and replicas is plaintext RESP2.
3. **Clustering & Distributed Quorum:** No Sentinel, Raft, Paxos, or cluster sharding. Failover is manual (`REPLICAOF NO ONE`).
4. **Synchronous Replication / `WAIT`:** Replication is asynchronous. Master acknowledgment does not guarantee replica persistence.
5. **Sandboxed Scripting (No Lua):** Antares does not support `EVAL` or Lua script execution.
6. **Cross-Datacenter Replication:** Replication assumes low-latency, reliable inter-node links.

---

## 4. Threat Vectors & Mitigations

| Threat Vector | Mechanism / Vulnerability | System Defense / Operator Mitigation |
|---|---|---|
| **Unauthorized Access** | Remote TCP access to unauthenticated Redis port | Server binds `127.0.0.1` by default. Operator must never bind `0.0.0.0` on an untrusted network without an authenticating TLS reverse proxy (e.g., Envoy, stunnel, WireGuard). |
| **SSRF via Observatory** | Exploitation of `/api/exec` to probe internal networks | Target address is strictly restricted to allowlisted aliases (`""`, `"antares"`, `"redis"`). Arbitrary host/port dials are rejected. |
| **DNS Rebinding & CSRF** | Malicious web page commanding local Observatory instance | Bridge middleware enforces Host header validation against loopback and validates `Origin` / `Referer` against `r.Host`. Destructive lab endpoints are gated behind `DEMO_MODE=1`. |
| **Parser Denial of Service** | Gigabyte-sized multibulk array or bulk string allocation | Payloads are capped at 512 MiB (`MaxBulkLen`). Declared lengths > 64 KiB do not pre-allocate; buffers grow incrementally with actual arriving TCP bytes. Multibulk counts are capped at `INT_MAX`, and aggregate request size is bounded to 1 GiB. |
| **Algorithmic Complexity (ReDoS/Glob)** | Pathological pattern matching like `KEYS a*a*a*a*a*b` | Glob engine enforces `nesting <= 1000` depth cap and Redis's `skipLongerMatches` backtracking pruning. Virtual SCAN bucket traversal is bounded at `min(10*COUNT, 65536)`. |
| **Memory Exhaustion via Long-Lived Conns** | Large query responses pinning huge encoder buffers | Connection encoders must shrink or drop buffers exceeding `encoderRetainCap` (64 KiB) on `Reset()`. |
| **AOF Tail Corruption / TOCTOU** | Power loss during append or concurrent file tampering | AOF files must be created with `0600` permissions. Frame CRC32 and length bounds detect tampering and torn writes. Directory fsync protects renames. |

---

## 5. Operator Requirements and Hardening Checklist

When deploying Antares in any environment beyond local testing:
1. **Network Binding:** Never launch with `-bind 0.0.0.0` on a public interface. Use a VPN or loopback interface with an authenticating, TLS-terminating sidecar.
2. **File Permissions:** Ensure the directory containing `-aof-file` and `-event-log` is owned exclusively by the `antares` user (`0700`) and created files are mode `0600`.
3. **Observatory Exposure:** Never expose Observatory port `6390` outside loopback. Do not enable `DEMO_MODE=1` in multi-tenant environments.
4. **Resource Constraints:** Impose OS-level cgroup memory limits (`docker run -m ...`) in addition to configuring `CONFIG SET maxmemory` to protect the host against unmanaged runtime allocations.
5. **Monitoring:** Monitor `aof_last_write_status` and `aof_last_bgrewrite_status` via `INFO persistence` and alert on any status other than `ok`.
