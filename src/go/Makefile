# Default version number to git commit hash if not set.
COMMIT := $(or $(COMMIT),$(shell git log -1 --format="%h"))
TAG    := $(or $(TAG),$(shell git log -1 --format="%h"))
DATE   := $(shell date -u)

SOURCES   := $(shell find . -name '*.go')
UISOURCES := $(shell find web -name '*.go' -not -path 'web/rbac/known_policy.go')
CONFIGS   := $(shell find api/config/default -name '*')
TEMPLATES := $(shell find tmpl/templates -name '*')

TUNNELSOURCES := $(shell find tunneler -name '*.go' )

THISFILE := $(lastword $(MAKEFILE_LIST))
THISDIR  := $(shell dirname $(realpath $(THISFILE)))
GOBIN    := $(THISDIR)/bin
# Use golangci-lint from PATH if available (e.g. CI), otherwise use local install
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2> /dev/null || echo $(GOBIN)/golangci-lint)

# Define a helper for checking command existence
check-command = @if ! command -v $(1) > /dev/null; then \
		echo "Error: '$(1)' not found. $(2)"; \
		exit 1; \
	fi

# Prepend this repo's bin directory to our path since we'll want to
# install some build tools there for use during the build process.
PATH := $(GOBIN):$(PATH)

# Export GOBIN env variable so `go install` picks it up correctly.
export GOBIN

.PHONY: all check check-protoc clean format generate generate-mocks generate-protobuf go-generate help install install-dev lint lint-clean phenix phenix-debug phenix-tunneler test
.DEFAULT_GOAL := help

help:
	@echo "Build targets:"
	@echo "  phenix             - Build the main phenix binary"
	@echo "  phenix-debug       - Build the phenix binary with debug symbols"
	@echo "  phenix-tunneler    - Build tunneler binaries"
	@echo ""
	@echo "Development targets:"
	@echo "  all                - Run all tools (format, lint, test)"
	@echo "  check              - Run linters without fixing (for CI)"
	@echo "  format             - Format code (golangci-lint)"
	@echo "  lint               - Lint code and fix issues (golangci-lint)"
	@echo "  lint-clean         - Clean lint cache"
	@echo "  test               - Run unit tests"
	@echo ""
	@echo "Code Generation targets:"
	@echo "  generate           - Run all code generation tools"
	@echo "  generate-mocks     - Generate mocks"
	@echo "  generate-protobuf  - Generate protobufs"
	@echo "  go-generate        - Run go generate"
	@echo ""
	@echo "Installation targets:"
	@echo "  install            - Install Go module dependencies"
	@echo "  install-dev        - Install development and build dependencies"
	@echo ""
	@echo "Cleanup targets:"
	@echo "  clean              - Clean artifacts"
	@echo ""
	@echo "Help targets:"
	@echo "  help               - Show this help message"

all: format lint test

check:
	$(call check-command,$(GOLANGCI_LINT),Please run 'make install-dev' to install it.)
	$(GOLANGCI_LINT) run -c .golangci.yml

generate: generate-mocks generate-protobuf go-generate

format:
	$(call check-command,$(GOLANGCI_LINT),Please run 'make install-dev' to install it.)
	$(GOLANGCI_LINT) fmt -c .golangci.yml

lint:
	$(call check-command,$(GOLANGCI_LINT),Please run 'make install-dev' to install it.)
	$(GOLANGCI_LINT) run -c .golangci.yml --fix

lint-clean:
	$(call check-command,$(GOLANGCI_LINT),Please run 'make install-dev' to install it.)
	$(GOLANGCI_LINT) cache clean

test:
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	go test -v -race ./...

clean: lint-clean
	$(RM) bin/phenix
	$(RM) bin/phenix-debug
	$(RM) store/mock.go
	$(RM) web/proto/*.pb.go
	$(RM) web/public/docs/index.html
	$(RM) web/public/favicon.ico
	$(RM) web/public/index.html
	$(RM) -r web/public/assets
	$(RM) web/rbac/known_policy.go
	go clean -testcache

install:
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	go mod tidy

install-dev: bin/mockgen bin/protoc-gen-go
	$(call check-command,curl,Please install curl (e.g. sudo apt install curl))
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(GOBIN) v2.10.1

bin/mockgen:
	go install github.com/golang/mock/mockgen

bin/protoc-gen-go:
	go install google.golang.org/protobuf/cmd/protoc-gen-go

web/public/docs/index.html: web/public/docs/openapi.yml
	npx redoc-cli build web/public/docs/openapi.yml -o web/public/docs/index.html --title 'phenix API'

web/public/index.html:
	$(MAKE) -C ../js dist/index.html
	cp -a ../js/dist/* web/public

generate-mocks: store/mock.go

store/mock.go: store/store.go bin/mockgen
	$(GOBIN)/mockgen -self_package phenix/store -destination store/mock.go -package store phenix/store Store

check-protoc:
	@command -v protoc >/dev/null 2>&1 || { echo "Error: protoc is not installed. Please install protobuf-compiler."; exit 1; }

generate-protobuf: check-protoc web/proto/experiment.pb.go web/proto/host.pb.go web/proto/user.pb.go web/proto/role.pb.go web/proto/vm.pb.go

web/proto/experiment.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/experiment.proto

web/proto/host.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/host.proto

web/proto/user.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/user.proto

web/proto/role.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/role.proto

web/proto/vm.pb.go: web/proto/*.proto bin/protoc-gen-go
	protoc -I . -I web/proto --go_out=paths=source_relative:. ./web/proto/vm.proto

go-generate: web/rbac/known_policy.go

web/rbac/known_policy.go: $(UISOURCES)
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	go generate web/rbac/known_policy_gen.go

phenix: bin/phenix
bin/phenix: $(SOURCES) generate
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -X 'phenix/version.Tag=$(TAG)' -X 'phenix/version.Date=$(DATE)' -s -w" -trimpath -o bin/phenix main.go

phenix-debug: bin/phenix-debug
bin/phenix-debug: $(SOURCES) generate
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -gcflags "all=-N -l" -trimpath -o bin/phenix-debug main.go

phenix-tunneler: bin/phenix-tunneler-linux-amd64 bin/phenix-tunneler-darwin-arm64 bin/phenix-tunneler-darwin-amd64 bin/phenix-tunneler-windows-amd64.exe

bin/phenix-tunneler-linux-amd64: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-linux-amd64 $(TUNNELSOURCES)

bin/phenix-tunneler-darwin-arm64: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-darwin-arm64 $(TUNNELSOURCES)

bin/phenix-tunneler-darwin-amd64: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-darwin-amd64 $(TUNNELSOURCES)

bin/phenix-tunneler-windows-amd64.exe: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -a -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-windows-amd64.exe $(TUNNELSOURCES)
