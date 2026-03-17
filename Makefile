# Spore Makefile

.PHONY: build run test clean

BINARY := spore
VERSION := 0.1.0-dev

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/spore

run: build
	./bin/$(BINARY) run

test:
	go test ./...

clean:
	rm -rf bin/

init:
	./bin/$(BINARY) init

fmt:
	go fmt ./...
	
lint:
	golangci-lint run

.DEFAULT_GOAL := build
