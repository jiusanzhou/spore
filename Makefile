# Spore Makefile
# Inspired by kimbot

# Go command to use for build
GO ?= $(shell command -v go 2>/dev/null || echo "/usr/local/go/bin/go")
INSTALL ?= install

LDFLAGS := $(shell $(GO) run -mod=readonly go.zoe.im/x/version/gen 2>/dev/null || echo "")

ifneq "$(strip $(shell command -v $(GO) 2>/dev/null))" ""
	GOOS ?= $(shell $(GO) env GOOS)
	GOARCH ?= $(shell $(GO) env GOARCH)
else
	ifeq ($(GOOS),)
		ifeq ($(OS),Windows_NT)
			GOOS = windows
		else
			UNAME_S := $(shell uname -s)
			ifeq ($(UNAME_S),Linux)
				GOOS = linux
			endif
			ifeq ($(UNAME_S),Darwin)
				GOOS = darwin
			endif
			ifeq ($(UNAME_S),FreeBSD)
				GOOS = freebsd
			endif
		endif
	else
		GOOS ?= $$GOOS
		GOARCH ?= $$GOARCH
	endif
endif

ifndef GODEBUG
	EXTRA_LDFLAGS += -s -w
	DEBUG_GO_GCFLAGS :=
	DEBUG_TAGS :=
else
	DEBUG_GO_GCFLAGS := -gcflags=all="-N -l"
	DEBUG_TAGS := static_build
endif

GO_BUILD_FLAGS = --ldflags '${LDFLAGS} ${EXTRA_LDFLAGS}'
GO_GCFLAGS=$(shell				\
	set -- ${GOPATHS};			\
	echo "-gcflags=-trimpath=$${1}/src";	\
	)

WHALE = "🦠"

# Project packages
PACKAGES=$(shell $(GO) list ${GO_TAGS} ./... 2>/dev/null | grep -v /vendor/)

# Project binaries - auto discover from cmd/
COMMANDS ?= $(shell ls -d ./cmd/*/ 2>/dev/null | sed 's|./cmd/||' | sed 's|/||')

BINARIES=$(addprefix bin/,$(COMMANDS))

FORCE:

define BUILD_BINARY
@echo "$(WHALE) $@"
$(GO) build ${DEBUG_GO_GCFLAGS} ${GO_GCFLAGS} ${GO_BUILD_FLAGS} -o $@ ${GO_LDFLAGS} ${GO_TAGS} ./$<
endef

.PHONY: all build binaries test test-short clean deps demo install fmt lint version help
.DEFAULT: default
.DEFAULT_GOAL := all

# Build a binary from a cmd
bin/%: cmd/% FORCE
	$(call BUILD_BINARY)

all: binaries

binaries: $(BINARIES) ## build binaries
	@echo "$(WHALE) $@"

build: binaries

test:
	@echo "Execute test"
	@$(GO) test ./...

test-short:
	@echo "Execute short test"
	@$(GO) test -short ./...

clean:
	@echo "Clean build artifacts"
	@rm -rf bin/

deps:
	@echo "Download dependencies"
	@$(GO) mod download
	@$(GO) mod tidy

fmt:
	@gofmt -w .

lint:
	@$(GO) vet ./...

# Demo: start a swarm with API dashboard
demo: binaries
	@echo "$(WHALE) Starting Spore swarm demo..."
	@echo "   Config: ~/.spore/config.toml"
	@echo "   Dashboard: http://localhost:8080"
	./bin/spore swarm -n 3 --api-port 8080

# Install to system
install: binaries
	@echo "Installing spore to system"
	@$(INSTALL) bin/spore /usr/local/bin/spore

# Generate version info
version:
	@echo "Spore version information:"
	@$(GO) run -mod=readonly go.zoe.im/x/version/gen 2>/dev/null || echo "Version: development"

help:
	@echo "Spore Build System"
	@echo ""
	@echo "Targets:"
	@echo "  all         build all binaries (default)"
	@echo "  binaries    build binaries"
	@echo "  test        execute tests"
	@echo "  test-short  execute tests (skip slow)"
	@echo "  clean       remove build artifacts"
	@echo "  deps        download and tidy dependencies"
	@echo "  demo        build and run swarm demo"
	@echo "  install     install spore to /usr/local/bin"
	@echo "  fmt         format Go source"
	@echo "  lint        run go vet"
	@echo "  version     show version information"
	@echo "  help        show this help"
	@echo ""
	@echo "Environment Variables:"
	@echo "  GO          go command to use (default: go)"
	@echo "  GOOS        target operating system"
	@echo "  GOARCH      target architecture"
	@echo "  GODEBUG     enable debug build"
