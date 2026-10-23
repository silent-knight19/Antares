# Antares + Observatory — build entry points (docs/PLAN.md §13).
# Core targets work as of P0.1; phase-gated targets fail loudly with a pointer.

SHELL := /bin/bash
GO ?= go

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

BIN := bin
ANTARES_BIN := $(BIN)/antares
OBSERVATORY_BIN := $(BIN)/observatory

FUZZTIME ?= 30s
REF_ADDR ?= 127.0.0.1:6379
PORT ?= 6380

.PHONY: help build test test-race vet fmt tidy run run-observatory clean fuzz \
	verify diff-test clock-check lin-check tcl-expire tcl-incr crash-test bench bench-repl tcl showcase showcase-dev showcase-e2e ui-dev ui-build seed

help:
	@echo "Antares targets:"
	@echo "  build            build server + observatory binaries into $(BIN)/"
	@echo "  test             run all Go tests"
	@echo "  test-race        run all Go tests with the race detector"
	@echo "  vet              run go vet"
	@echo "  fmt              format the tree with gofmt"
	@echo "  tidy             go mod tidy"
	@echo "  run              run the antares server on 127.0.0.1:6380 (flags: -port -max-conns -idle-timeout -appendonly -aof-file -appendfsync -aof-repair)"
	@echo "  run-observatory  run the observatory bridge (defaults: antares 6380, web/dist)"
	@echo "  clean            remove build artifacts"
	@echo "  fuzz             run the parser and AOF recovery fuzzers for FUZZTIME (default 30s, e.g. FUZZTIME=15m)"
	@echo "  diff-test        run the command differential harness against reference Redis (REF_ADDR=$(REF_ADDR))"
	@echo "  crash-test       build and run the SIGKILL acked-write harness (CRASH_ITERATIONS=100)"
	@echo "  clock-check      enforce the ADR-009 rule: no direct wall-clock reads outside clock/server I/O"
	@echo "  lin-check        run the Porcupine concurrent-register linearizability check"
	@echo "  bench            run throughput benchmarks vs Redis 7.4 and replication benchmarks"
	@echo "  bench-repl       run replication throughput/lag benchmarks only (no Docker required)"
	@echo "  tcl-expire       run the selected official Redis unit/expire tests (downloads pinned 7.4.11 tests)"
	@echo "  tcl-incr         run the selected official Redis unit/type/incr tests"
	@echo "  tcl              run both selected Tcl suites with a per-command report"
	@echo "  showcase-dev     build UI if present, then run Antares + Observatory locally"
	@echo "  ui-build         npm install + vite build web/"
	@echo "  seed             seed PORT (default 6380) with ~10k keys for the Keyspace view"
	@echo "  phase-gated:     verify bench showcase showcase-e2e ui-dev"

build:
	@mkdir -p $(BIN)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(ANTARES_BIN) ./cmd/antares
	$(GO) build -ldflags "$(LDFLAGS)" -o $(OBSERVATORY_BIN) ./cmd/observatory

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

clock-check:
	@matches=$$(grep -rn --include='*.go' -E 'time\.(Now|Since)\(' internal cmd | grep -v '^internal/clock/clock.go:' | grep -v '^internal/server/server.go:' | grep -v '_test.go:' || true); \
	if [ -n "$$matches" ]; then echo "$$matches"; echo "use internal/clock instead of direct wall-clock reads"; exit 1; fi

lin-check:
	$(GO) test -count=1 ./tests/linearizability/

fmt:
	gofmt -l -w .

tidy:
	$(GO) mod tidy

run:
	$(GO) run ./cmd/antares

run-observatory:
	$(GO) run ./cmd/observatory

clean:
	rm -rf $(BIN)

fuzz:
	$(GO) test -run='^$$' -fuzz=FuzzReadRequest -fuzztime=$(FUZZTIME) ./internal/protocol/
	$(GO) test -run='^$$' -fuzz=FuzzRecoverFile -fuzztime=$(FUZZTIME) ./internal/persist/

diff-test:
	docker compose up -d --wait redis-ref
	REDIS_REF_ADDR=$(REF_ADDR) $(GO) test -count=1 -run 'TestDiff' ./tests/differential/

crash-test: build
	ANTARES_CRASH_TEST=1 $(GO) test -count=1 -run TestCrashRecovery ./tests/chaos/

tcl-expire:
	@tests/tcl/run-expire.sh expire

tcl-incr:
	@tests/tcl/run-incr.sh

tcl: tcl-expire tcl-incr

ui-build:
	@if [ -f web/package.json ]; then cd web && npm install --no-audit --no-fund && npm run build; else echo "ui-build: web/package.json not found yet; bridge serves the placeholder page"; fi

showcase-dev: build ui-build
	@deploy/demo/run-dev.sh

seed:
	@deploy/demo/seed.sh $(PORT)

bench: build
	@WRITE_CLAIMS=1 ./bench/scripts/run-bench.sh
	@WRITE_CLAIMS=1 python3 ./bench/scripts/run-repl-bench.py

bench-repl: build
	@WRITE_CLAIMS=1 python3 ./bench/scripts/run-repl-bench.py

verify: test-race clock-check diff-test tcl crash-test lin-check
	@echo "==> Running fuzz smoke..."
	$(GO) test -run='^$$' -fuzz=FuzzReadRequest -fuzztime=5s ./internal/protocol/
	$(GO) test -run='^$$' -fuzz=FuzzRecoverFile -fuzztime=5s ./internal/persist/
	@echo "==> Running benchmarks and updating claims.json..."
	@BENCH_REQUESTS=20000 BENCH_RUNS=3 WRITE_CLAIMS=1 ./bench/scripts/run-bench.sh
	@REPL_REQUESTS=500000 REPL_RUNS=3 WRITE_CLAIMS=1 python3 ./bench/scripts/run-repl-bench.py
	@echo "==> Verification complete: all tests, diffs, Tcl, crash-safety, fuzz, and benchmarks green!"

showcase: build ui-build
	@deploy/demo/run-dev.sh

ui-dev:
	cd web && npm run dev

showcase-e2e:
	@echo "make $@: not implemented yet — arrives at U12 in docs/PLAN.md §6"
	@exit 1
