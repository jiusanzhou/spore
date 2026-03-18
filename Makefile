# Spore Makefile

.PHONY: build test test-short clean demo install fmt lint

VERSION ?= 0.1.0-dev
BINARY = bin/spore
LDFLAGS = -ldflags "-X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/spore

test:
	go test ./...

test-short:
	go test -short ./...

clean:
	rm -rf bin/

install: build
	cp $(BINARY) $(GOPATH)/bin/spore 2>/dev/null || cp $(BINARY) /usr/local/bin/spore

demo: build
	@echo "🦠 Starting Spore swarm demo..."
	@echo "   Set SPORE_LLM_API_KEY or OPENAI_API_KEY for full LLM integration"
	./$(BINARY) swarm -n 3 -m gpt-4o-mini --api-port 8080

fmt:
	gofmt -w .

lint:
	go vet ./...

.DEFAULT_GOAL := build
