SHELL := /bin/bash
export GOTOOLCHAIN := local
export PATH := $(PATH):$(HOME)/go/bin
SERVICES := bot core payments provisioner
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version.Version=$(VERSION)

.PHONY: all build test lint vuln tidy fmt clean $(SERVICES)
all: fmt lint test build

build:
	@mkdir -p bin
	@for s in $(SERVICES) bobres; do echo "build $$s"; go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$$s ./cmd/$$s || exit 1; done

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

vuln:
	@command -v govulncheck >/dev/null || go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -rf bin
