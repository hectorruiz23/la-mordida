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

# The acceptance scenario of the plan: it is built by the card that ships the
# web surface, and it has to prove the integrated app over local fixtures.
scenario:
	@echo "make scenario is not implemented yet: it arrives with the card that serves the rankings"; exit 1

clean:
	rm -rf bin
