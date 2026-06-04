VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS    := -w -s -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)

GOSEC_FLAGS    = -quiet
# CI gate: fail only on HIGH severity findings.
GOSEC_CI_FLAGS = $(GOSEC_FLAGS) -severity=high

.PHONY: all build build-all run fmt vet test check clean sec sec-full install

all: check build

# Native build with version metadata injected.
build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/pgbackup .

# Cross-compile every target via the build script.
build-all:
	VERSION=$(VERSION) ./scripts/build.sh

# Run against the example config (override CONFIG=... as needed).
run: build
	./bin/pgbackup -config pg_backup.example.yaml -dry-run

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

# Everything CI should gate on, in one target.
check: fmt vet test

clean:
	rm -rf bin

# Security scan — fails only on HIGH severity (CI gate).
sec:
	go run github.com/securego/gosec/v2/cmd/gosec@latest $(GOSEC_CI_FLAGS) ./...

# Full scan — all severities. Useful locally for cleanup.
sec-full:
	go run github.com/securego/gosec/v2/cmd/gosec@latest $(GOSEC_FLAGS) ./...

# Install the native binary to /usr/local/bin (needs write permission).
install: build
	install -m 0755 bin/pgbackup /usr/local/bin/pgbackup
