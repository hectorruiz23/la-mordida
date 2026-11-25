GO ?= go
BIN := bin/contratos
PI_GOARCH ?= arm64

.PHONY: verify fmt build test scenario build-pi clean

# What CI runs and what the board requires before a pull request may land.
verify: fmt build test

fmt:
	@files=$$(git ls-files '*.go'); \
	if [ -n "$$files" ]; then \
		out=$$(gofmt -l $$files); \
		if [ -n "$$out" ]; then echo "gofmt wants these files formatted:"; echo "$$out"; exit 1; fi; \
	fi

build:
	$(GO) build -o $(BIN) ./cmd/contratos

# The Pi is aarch64 and the binary has no cgo: one static file, no toolchain or
# shared libraries on the target.
build-pi:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(PI_GOARCH) $(GO) build -trimpath -ldflags '-s -w' -o $(BIN)-linux-$(PI_GOARCH) ./cmd/contratos

test:
	$(GO) vet ./...
	$(GO) test ./...

# The acceptance scenario of the plan: import the repository fixtures, start
# the real binary and prove both rankings over HTTP.
scenario: build
	@./scripts/scenario.sh

clean:
	rm -rf bin
