.PHONY: build test clean run swarm docker

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X go.zoe.im/x/version.Version=$(VERSION)

build:
	@mkdir -p bin
	CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o bin/spore ./cmd/spore
	@echo "✅ bin/spore built ($(VERSION))"

test:
	go test ./... -timeout 120s

clean:
	rm -rf bin/ dist/

run: build
	./bin/spore run

swarm: build
	./bin/spore swarm -d examples/consciousness-demo --api-port 9292

docker:
	docker build -t spore:$(VERSION) .

frontend:
	cd interactive-web-app && npm ci && npx react-scripts build

lint:
	@which golangci-lint > /dev/null 2>&1 || echo "install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"
	golangci-lint run ./...
