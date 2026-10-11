SHELL := /bin/bash
export GOTOOLCHAIN := local
export PATH := $(PATH):$(HOME)/go/bin
SERVICES := bot core payments provisioner
# Same version as the CI "Lint" job; golangci-lint v1 cannot lint a Go 1.26 module.
GOLANGCI_LINT_VERSION := v2.14.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version.Version=$(VERSION)

.PHONY: all build test lint vuln tidy fmt clean proto proto-lint dashboard dashboard-test $(SERVICES)
all: fmt lint test build

proto:
	@command -v buf >/dev/null || (echo "buf not found: go install github.com/bufbuild/buf/cmd/buf@latest" && exit 1)
	cd proto && buf generate

proto-lint:
	cd proto && buf lint

# The web dashboard (needs Node.js). Run before `make build`: core embeds what
# web/dashboard/dist/app holds, or shows a "not built" notice at /admin.
dashboard:
	cd web/dashboard && npm ci --no-audit --no-fund && npm run build

dashboard-test:
	cd web/dashboard && npm run typecheck && npm test

build:
	@mkdir -p bin
	@for s in $(SERVICES) bobres; do echo "build $$s"; go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$$s ./cmd/$$s || exit 1; done

test:
	go test -race -count=1 ./...

lint:
	@golangci-lint version 2>/dev/null | grep -q 'has version v\{0,1\}2\.' || { echo "golangci-lint v2 is required: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; exit 1; }
	golangci-lint run ./...

vuln:
	@command -v govulncheck >/dev/null || go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -rf bin web/dashboard/dist/app
