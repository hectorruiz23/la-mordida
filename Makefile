GO ?= go
BIN := bin/contratos

.PHONY: verify fmt build test scenario clean

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

test:
	$(GO) vet ./...
	$(GO) test ./...

# The acceptance scenario of the plan: import the repository fixtures, start
# the real binary and prove both rankings over HTTP.
scenario: build
	@./scripts/scenario.sh

clean:
	rm -rf bin
