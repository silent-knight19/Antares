# Antares Operational Runbook (PLAN P10.1)

> **Antares**: A Redis-compatible high-performance server built from scratch in Go.
> This runbook outlines local setup, containerized deployment, configuration flags, operational commands, and troubleshooting procedures.

---

## 1. Quick Start (< 5 Minutes)

### Option A: Using Docker Compose (Zero Go Toolchain Required)

```bash
# Clone the repository
git clone https://github.com/silent-knight19/Antares.git
cd Antares

# Start Antares server (port 6380) and reference Redis 7.4 (port 6379)
docker compose up -d

# Verify connectivity using redis-cli
redis-cli -p 6380 PING
# Output: PONG

# Write and read data
redis-cli -p 6380 SET hello "world"
redis-cli -p 6380 GET hello
# Output: "world"
```

### Option B: Local Go Build (Fastest for Development)

Prerequisites: **Go 1.27.1+**

```bash
# Build binaries into bin/
make build

# Run Antares server locally
./bin/antares -port 6380
```

---

## 2. Server Configuration Flags

Antares accepts the following CLI flags with Redis-compatible defaults:

| Flag | Default | Description |
| :--- | :---: | :--- |
| `-bind` | `127.0.0.1` | Network interface to bind to (e.g. `0.0.0.0` for all interfaces) |
| `-port` | `6380` | TCP port to listen on (`6380` avoids collision with default Redis `6379`) |
| `-max-conns` | `10000` | Maximum simultaneous client connections (enforces Redis maxclients error) |
| `-idle-timeout` | `0s` | Disconnect clients idle longer than this duration (`0s` = disabled) |
| `-appendonly` | `no` | Enable framed AOF persistence (`yes` or `no`) |
| `-aof-file` | `appendonly.aof` | Path to the append-only log file |
| `-appendfsync` | `everysec` | Fsync policy: `always` (zero acked-write loss), `everysec`, or `no` |
| `-aof-repair` | `false` | Discard corrupt tail on restart instead of refusing to start |
| `-event-log` | `""` | Path to append-only JSONL event stream tap for the Observatory |
| `--version` | — | Print server version and git commit hash |

---

## 3. Operational Verification & Testing

The repository provides a single-command verification suite matching the Definition of Done:

```bash
# Run the complete verification pyramid (tests, clock checks, diff, Tcl, crash, fuzz, benchmarks)
make verify

# Run individual verification targets
make test-race        # All unit tests with Go race detector
make clock-check      # Enforce ADR-009 wall-clock boundaries
make diff-test        # Differential suite vs pinned Redis 7.4.11
make tcl              # Official Redis 7.4.11 test suite (65/65 pass)
make crash-test       # 100x SIGKILL AOF durability test
make fuzz             # RESP parser & frame recovery fuzzing
make bench            # redis-benchmark throughput & claims generation
```

---

## 4. Observatory Dev Console & UI

Antares includes the **Observatory** dev console (bridge + SPA):

```bash
# Run both Antares and the Observatory UI locally
make showcase-dev

# Open in browser:
# http://localhost:6390
```

---

## 5. Troubleshooting & FAQ

### Port Collision (Address already in use)
* **Symptom**: `listen tcp 127.0.0.1:6380: bind: address already in use`
* **Fix**: Ensure another Antares instance is not running (`killall antares` or `lsof -i :6380`).

### AOF Corruption Refusal on Startup
* **Symptom**: `persist: checksum mismatch at byte ... (repair discards the tail): corrupt log`
* **Diagnosis**: Power loss or crash during partial disk write caused a corrupt frame.
* **Fix**: Start the server with `-aof-repair` to discard the corrupt tail and recover all preceding valid frames.

### Memory Limit Reached (`-OOM command not allowed...`)
* **Symptom**: Mutating commands return `-OOM command not allowed when used memory > 'maxmemory'`
* **Diagnosis**: Keyspace exceeded the configured `maxmemory` limit.
* **Fix**: Either adjust the memory limit dynamically:
  ```bash
  redis-cli -p 6380 CONFIG SET maxmemory 256mb
  ```
  Or enable eviction:
  ```bash
  redis-cli -p 6380 CONFIG SET maxmemory-policy allkeys-lru
  ```
