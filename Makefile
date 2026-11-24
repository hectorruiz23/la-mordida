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

# The acceptance scenario of the plan. Until the rankings are served there is
# nothing to rank: it only proves the binary answers. The card that ships the
# web surface replaces this with the real scenario over local fixtures.
scenario: build
	@./$(BIN) --help > /dev/null || { echo "scenario: the binary does not answer --help"; exit 1; }
	@echo "scenario: the rankings are not served yet; the binary answers --help"

clean:
	rm -rf bin
