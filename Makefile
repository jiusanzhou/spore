.PHONY: build build-go build-all test clean run swarm docker release release-snapshot lint web-build web-dev

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
TREE_STATE ?= $(shell test -z "$$(git status --porcelain 2>/dev/null)" && echo "clean" || echo "dirty")
LDFLAGS := -s -w \
           -X go.zoe.im/x/version.GitVersion=$(VERSION) \
           -X go.zoe.im/x/version.GitCommit=$(COMMIT) \
           -X go.zoe.im/x/version.GitTreeState=$(TREE_STATE) \
           -X go.zoe.im/x/version.BuildDate=$(DATE)

build: web-build
	@mkdir -p bin
	CGO_ENABLED=1 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/spore ./cmd/spore
	@echo "✅ bin/spore built ($(VERSION))"

build-go:
	@mkdir -p bin
	CGO_ENABLED=1 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/spore ./cmd/spore
	@echo "✅ bin/spore built ($(VERSION), no frontend rebuild)"

# Build ALL three shipping binaries — spore CLI + ACP server + MCP server.
# This is what `release` produces; useful locally to verify everything
# compiles before tagging.
build-all: web-build
	@mkdir -p bin
	CGO_ENABLED=1 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/spore             ./cmd/spore
	CGO_ENABLED=1 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/spore-acp-server  ./cmd/spore-acp-server
	CGO_ENABLED=1 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/spore-mcp-server  ./cmd/spore-mcp-server
	@echo "✅ bin/{spore,spore-acp-server,spore-mcp-server} built ($(VERSION))"

test:
	go test ./... -timeout 120s

clean:
	rm -rf bin/ dist/

run: build
	./bin/spore run

swarm: build
	./bin/spore swarm -d examples/consciousness-demo --api-port 9292

docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(DATE) \
		-t spore:$(VERSION) -t spore:latest .

# Local snapshot — does NOT push anything. Useful to verify the
# goreleaser config before tagging.
#
# Requires:
#   brew install goreleaser
#   brew install zig          # for linux cross-compile (CGO needs a cross C compiler)
release-snapshot:
	goreleaser release --snapshot --clean --skip=publish,sign

# Real release. Requires a v* tag on HEAD and GITHUB_TOKEN with repo:write.
release:
	goreleaser release --clean

web-build:
	@cd web-v2 && pnpm install --frozen-lockfile && pnpm build
	@rm -rf web/dist && cp -r web-v2/dist web/dist
	@echo "✅ web/dist built (Vite + Tailwind v4 from web-v2/)"

web-dev:
	@cd web-v2 && pnpm dev

lint:
	@which golangci-lint > /dev/null 2>&1 || echo "install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"
	golangci-lint run ./...
