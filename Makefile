.PHONY: all build check clean docker examples example-go example-python format generate help help-all install-dev install-wrapper uninstall-wrapper lint run-examples test tunneler ui version
.DEFAULT_GOAL := help

# Define a helper for checking command existence
check-command = @if ! command -v $(1) > /dev/null; then \
		echo "Error: '$(1)' not found. $(2)"; \
		exit 1; \
	fi

help:
	@echo "Available targets:"
	@echo ""
	@echo "Development:"
	@echo "  all          - Run all development tasks (format, lint, test) and build examples"
	@echo "  check        - Run linters without fixing (for CI)"
	@echo "  format       - Format code"
	@echo "  generate     - Run code generation (protobuf, mocks, etc)"
	@echo "  lint         - Run linters and fix issues"
	@echo "  test         - Run unit tests"
	@echo "  examples     - Build/Check example applications"
	@echo "  run-examples - Run example applications"
	@echo ""
	@echo "Build:"
	@echo "  build        - Build the main phenix binary"
	@echo "  docker       - Build the phenix docker image"
	@echo "  ui           - Build the frontend UI"
	@echo "  tunneler     - Build phenix-tunneler binaries"
	@echo ""
	@echo "Installation:"
	@echo "  install-dev  - Install development and build dependencies"
	@echo "  install-wrapper - Install the Docker wrapper script (required for shell completion)"
	@echo "  uninstall-wrapper - Uninstall the Docker wrapper script"
	@echo ""
	@echo "Cleanup:"
	@echo "  clean        - Clean build artifacts"
	@echo ""
	@echo "Help:"
	@echo "  help         - Show this help message"
	@echo "  help-all     - Show help for all sub-projects"
	@echo "  version      - Show versions of installed tools"

all: format lint test examples

build: bin/phenix

docker:
	$(call check-command,docker,Please install Docker (https://docs.docker.com/get-docker/))
	@docker info > /dev/null 2>&1 || { echo "Docker daemon is not running"; exit 1; }
	docker build -t phenix -f docker/Dockerfile .

clean:
	$(RM) bin/phenix
	$(MAKE) -C src/go clean
	$(MAKE) -C src/js clean
	$(MAKE) -C examples clean

check: generate
	$(MAKE) -C src/go check
	$(MAKE) -C examples check

test: generate
	$(MAKE) -C src/go test
	$(MAKE) -C examples test

lint: generate
	$(MAKE) -C src/go lint
	$(MAKE) -C examples lint

format:
	$(MAKE) -C src/go format
	$(MAKE) -C examples format

generate:
	$(MAKE) -C src/go generate

install-dev:
	$(call check-command,go,Please install Go 1.24+ (https://go.dev/doc/install))
	$(call check-command,protoc,Please install protobuf-compiler (e.g. sudo apt install protobuf-compiler))
	$(call check-command,npm,Please install npm (e.g. sudo apt install npm))
	$(call check-command,yarn,Please install yarn (e.g. sudo npm install -g yarn))
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install go.uber.org/mock/mockgen@latest
	$(MAKE) -C src/go install-dev
	$(MAKE) -C examples install-dev

install-wrapper:
	@echo "Installing wrapper script to /usr/local/bin/phenix (requires sudo)..."
	@sudo install -m 0755 scripts/phenix-wrapper.sh /usr/local/bin/phenix
	@echo "Done."
	@echo ""
	@echo "NOTE: If you have an active alias for 'phenix', run 'unalias phenix' so the wrapper script is used."
	@echo ""
	@echo "To enable shell completion, add the following to your shell profile (e.g. ~/.bashrc, ~/.zshrc):"
	@echo "  source <(phenix completion bash)  # for bash"
	@echo "  source <(phenix completion zsh)   # for zsh"

uninstall-wrapper:
	@echo "Uninstalling /usr/local/bin/phenix (requires sudo)..."
	@if [ -f /usr/local/bin/phenix ]; then \
		sudo rm /usr/local/bin/phenix; \
		echo "Done."; \
	else \
		echo "/usr/local/bin/phenix not found."; \
	fi

tunneler:
	$(MAKE) -C src/go phenix-tunneler

ui:
	$(call check-command,yarn,Please install yarn (e.g. sudo npm install -g yarn))
	$(MAKE) -C src/js dist/index.html

bin/phenix: generate $(if $(SKIP_UI),,ui)
	$(call check-command,go,Please install Go 1.24+)
	cp -a src/js/dist/* src/go/web/public
	$(MAKE) -C src/go phenix
	mkdir -p bin
	cp src/go/bin/phenix bin/phenix

examples: generate
	$(MAKE) -C examples all

example-go: generate
	$(MAKE) -C examples example-go

example-python:
	$(MAKE) -C examples example-python

run-examples: generate
	$(MAKE) -C examples run

help-all: help
	@echo ""
	@echo "----------------------------------------------------------------"
	@echo "src/go targets:"
	@$(MAKE) -C src/go help
	@echo ""
	@echo "----------------------------------------------------------------"
	@echo "src/js targets:"
	@$(MAKE) -C src/js help
	@echo ""
	@echo "----------------------------------------------------------------"
	@echo "examples targets:"
	@$(MAKE) -C examples help

version:
	@echo "Tools Versions:"
	@echo "----------------------------------------------------------------"
	@printf "Go:         " && (go version 2>/dev/null || echo "Not installed")
	@printf "Python:     " && (python3 --version 2>/dev/null || echo "Not installed")
	@printf "Node:       " && (node --version 2>/dev/null || echo "Not installed")
	@printf "NPM:        " && (npm --version 2>/dev/null || echo "Not installed")
	@printf "Yarn:       " && (yarn --version 2>/dev/null || echo "Not installed")
	@printf "Protoc:     " && (protoc --version 2>/dev/null || echo "Not installed")
	@printf "Docker:     " && (docker --version 2>/dev/null || echo "Not installed")
