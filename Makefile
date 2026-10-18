# Antares + Observatory — build entry points (docs/PLAN.md §13).
# Core targets work as of P0.1; phase-gated targets fail loudly with a pointer.

SHELL := /bin/bash
GO ?= go

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

BIN := bin
ANTARES_BIN := $(BIN)/antares
OBSERVATORY_BIN := $(BIN)/observatory

.PHONY: help build test test-race vet fmt tidy run run-observatory clean \
	verify diff-test crash-test fuzz bench tcl showcase showcase-e2e ui-dev

help:
	@echo "Antares targets:"
	@echo "  build            build server + observatory binaries into $(BIN)/"
	@echo "  test             run all Go tests"
	@echo "  test-race        run all Go tests with the race detector"
	@echo "  vet              run go vet"
	@echo "  fmt              format the tree with gofmt"
	@echo "  tidy             go mod tidy"
	@echo "  run              run the antares server on 127.0.0.1:6380 (flags: -port -max-conns -idle-timeout)"
	@echo "  run-observatory  run the observatory bridge (stub until U2)"
	@echo "  clean            remove build artifacts"
	@echo "  phase-gated:     verify diff-test crash-test fuzz bench tcl showcase showcase-e2e ui-dev"

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

verify diff-test crash-test fuzz bench tcl showcase showcase-e2e ui-dev:
	@echo "make $@: not implemented yet — arrives at the phase marked in docs/PLAN.md §5–§7 (DoD: §2)"
	@exit 1
