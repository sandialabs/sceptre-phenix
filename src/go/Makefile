# Default build metadata to embedded values generated at build time.
COMMIT := $(or $(COMMIT),$(shell git log -1 --format="%h" 2>/dev/null))
TAG    := $(or $(TAG),$(shell git tag --points-at HEAD 2>/dev/null | head -n 1),$(shell git branch --show-current 2>/dev/null),$(COMMIT))
REPO   := $(or $(REPO),$(shell git config --get remote.origin.url 2>/dev/null | awk -F'github.com[:/]' '{print $$2}' | sed 's/\.git$$//'))
DATE   := $(shell date -u)

SOURCES   := $(shell find . -name '*.go')
UISOURCES := $(shell find web -name '*.go' -not -path 'web/rbac/known_policy.go')

TUNNELSOURCES := $(shell find tunneler -name '*.go' )

THISFILE := $(lastword $(MAKEFILE_LIST))
THISDIR  := $(shell dirname $(realpath $(THISFILE)))
GOBIN    := $(THISDIR)/bin

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

.PHONY: all api-docs check-protoc clean cli-docs deadcode deadcode-tests fuzz generate generate-builder-schema generate-mocks generate-protobuf generate-schemas go-generate help install install-dev phenix phenix-debug phenix-tunneler test test-cover test-stress
.DEFAULT_GOAL := help

help:
	@echo "Build targets:"
	@echo "  phenix             - Build the main phenix binary"
	@echo "  phenix-debug       - Build the phenix binary with debug symbols"
	@echo "  phenix-tunneler    - Build tunneler binaries"
	@echo ""
	@echo "Development targets:"
	@echo "  all                - Run all tools (test)"
	@echo "  test               - Run unit tests (V=1 for verbose output,"
	@echo "                       COVERPROFILE=<file> to write a coverage profile)"
	@echo "  test-cover         - Run unit tests into coverage.out and print the total"
	@echo "  test-stress        - Run the concurrency-heavy packages STRESS_COUNT times with -race"
	@echo "  fuzz               - Run every Go fuzz target for FUZZTIME (default 30s) each"
	@echo "  deadcode           - Fail on unreachable functions not in deadcode-allow.txt"
	@echo "  deadcode-tests     - List functions that only tests reach (advice only)"
	@echo ""
	@echo "NOTE: Linting/formatting (golangci-lint) is handled by 'prek' from"
	@echo "      the repository root, not by this Makefile. Run 'make lint'"
	@echo "      (or 'prek run --all-files') from the repo root."
	@echo ""
	@echo "Code Generation targets:"
	@echo "  generate           - Run all code generation tools"
	@echo "  generate-mocks     - Generate mocks"
	@echo "  generate-protobuf  - Generate protobufs"
	@echo "  generate-schemas   - Export the JSON schemas in schemas/ for editors and SchemaStore"
	@echo "  go-generate        - Run go generate"
	@echo "  api-docs           - Render the API reference (web/public/docs/index.html)"
	@echo "  cli-docs           - Regenerate the tracked CLI reference (docs/content/cli)"
	@echo "  generate-builder-schema - Write the web UI's Builder schema bundle and error codes"
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

all: test

generate: generate-mocks generate-protobuf go-generate generate-schemas generate-builder-schema

# The committed schemas/ files, exported from the embedded OpenAPI schemas.
# CI regenerates them and fails when the result differs from what is committed.
generate-schemas:
	go run ./tools/schema-export -out ../../schemas

# The web UI's copy of the Builder document schema, which embeds the v1
# and v2 config schemas, is committed, and so is its copy of the Builder
# error codes. CI regenerates them and fails when the result differs from
# what is committed. Only a Go of phenix's
# Unicode version writes the copy of Go's case mapping (case_test.go).
BUILDER_SCHEMA_TESTS := TestFrontendSchemaBundleMatchesSchemaJSON|TestFrontendCodesMatchRegistry|TestFrontendCaseTableMatchesUnicode|TestFoldCorpus

# The Docker and Podman builds run make generate in a copy of src/go without
# src/js. They build the web UI from the committed copies, so they skip this.
generate-builder-schema:
ifneq ($(wildcard $(THISDIR)/../js/src/builder/schema/.),)
	go test ./types/builder -run '^($(BUILDER_SCHEMA_TESTS))$$' -count=1 -update-frontend-schema
else
	@echo "generate-builder-schema: no src/js/src/builder/schema, so nothing to write"
endif

# Shuffled test order exposes tests that depend on each other; a failing
# package prints the -test.shuffle seed to rerun it with.
test:
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	go test $(if $(V),-v) -race -shuffle=on $(if $(COVERPROFILE),-coverprofile=$(COVERPROFILE)) ./...

# Packages whose shared state many goroutines touch: grants, the job registry,
# the freshness sequence and the audit writer.
STRESS_COUNT ?= 20
STRESS_PKGS  ?= ./api/job/... ./util/freshness/... ./web/audit/... ./web/capability/... ./web/middleware/...

test-stress:
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	go test -race -shuffle=on -count=$(STRESS_COUNT) $(STRESS_PKGS)

# Each target fuzzes for FUZZTIME; a failing input lands in its testdata/fuzz/.
FUZZTIME ?= 30s
export FUZZTIME

fuzz:
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	../../scripts/go-fuzz.sh

deadcode: generate
	../../scripts/go-deadcode.sh

# deadcode-tests runs the deadcode version that scripts/go-deadcode.sh pins.
DEADCODE_VERSION := $(shell sed -n 's/^DEADCODE_VERSION=//p' ../../scripts/go-deadcode.sh 2>/dev/null)
DEADCODE         := go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)
DEADCODE_FMT     := {{range .Funcs}}{{printf "%s.%s %s\n" $$.Path .Name .Position}}{{end}}

# Functions that only tests reach. Test helper packages (*test) are left out.
deadcode-tests: generate
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	@prod=$$($(DEADCODE) -f '$(DEADCODE_FMT)' ./...) || exit 1; \
	test=$$($(DEADCODE) -test -f '$(DEADCODE_FMT)' ./...) || exit 1; \
	{ printf '%s\n' "$$test"; echo '--'; printf '%s\n' "$$prod"; } | awk ' \
		$$0 == "--" { prod = 1; next } \
		!prod { dead[$$1]; next } \
		{ pkg = $$1; sub(/\.[^\/]*$$/, "", pkg) } \
		NF && !($$1 in dead) && pkg !~ /test(\/|$$)/ { print "reached only from tests: " $$0 }'

test-cover:
	$(MAKE) test COVERPROFILE=coverage.out
	go tool cover -func=coverage.out | tail -n 1

clean:
	$(RM) bin/phenix
	$(RM) bin/phenix-debug
	$(RM) store/mock.go
	$(RM) web/proto/*.pb.go
	$(RM) web/public/docs/index.html
	$(RM) web/public/favicon*.ico
	$(RM) web/public/index.html
	$(RM) web/public/builder-assets.json
	$(RM) -r web/public/assets
	$(RM) web/rbac/known_policy.go
	go clean -testcache

install:
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	go mod tidy

install-dev: bin/mockgen bin/protoc-gen-go
	$(call check-command,curl,Please install curl (e.g. sudo apt install curl))
	# prek runs golangci-lint from its own isolated, version-pinned install
	# (see prek.toml) and doesn't put it on PATH, so this system-wide install
	# is what IDEs/editors (e.g. the VS Code Go extension, GoLand) pick up for
	# real-time linting, and what you get running `golangci-lint` directly for
	# quick ad-hoc checks. Keep the version in sync with prek.toml/../prek.toml
	# so editor and manual runs match prek/CI results.
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(GOBIN) v2.10.1

bin/mockgen:
	go install github.com/golang/mock/mockgen

bin/protoc-gen-go:
	go install google.golang.org/protobuf/cmd/protoc-gen-go

api-docs: web/public/docs/index.html

# Unlike the outputs above, the CLI reference is tracked; check-generated fails when it is stale.
cli-docs: generate
	go run ./cmd/gendocs -out ../../docs/content/cli -mkdocs ../../docs/mkdocs.yml

# The Redocly version is pinned in scripts/api-docs.sh, shared with every
# other place that renders the API reference.
web/public/docs/index.html: web/public/docs/openapi.yml ../../scripts/api-docs.sh
	../../scripts/api-docs.sh build web/public/docs/openapi.yml web/public/docs/index.html

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
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -X 'phenix/version.Tag=$(TAG)' -X 'phenix/version.Date=$(DATE)' -X 'phenix/version.Repo=$(REPO)' -s -w" -trimpath -o bin/phenix main.go

phenix-debug: bin/phenix-debug
bin/phenix-debug: $(SOURCES) generate
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -gcflags "all=-N -l" -trimpath -o bin/phenix-debug main.go

phenix-tunneler: bin/phenix-tunneler-linux-amd64 bin/phenix-tunneler-darwin-arm64 bin/phenix-tunneler-darwin-amd64 bin/phenix-tunneler-windows-amd64.exe

bin/phenix-tunneler-linux-amd64: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-linux-amd64 $(TUNNELSOURCES)

bin/phenix-tunneler-darwin-arm64: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-darwin-arm64 $(TUNNELSOURCES)

bin/phenix-tunneler-darwin-amd64: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-darwin-amd64 $(TUNNELSOURCES)

bin/phenix-tunneler-windows-amd64.exe: $(TUNNELSOURCES)
	mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-X 'phenix/version.Commit=$(COMMIT)' -s -w" -trimpath -o bin/phenix-tunneler-windows-amd64.exe $(TUNNELSOURCES)
