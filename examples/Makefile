.PHONY: all build check clean clean-venv example-go example-python format help install-dev lint run run-go run-python test
.DEFAULT_GOAL := help

GO ?= go
SYSTEM_PYTHON ?= python3
VENV := .venv
PYTHON := $(VENV)/bin/python
PIP := $(VENV)/bin/pip
PYTEST := $(VENV)/bin/pytest
FLAKE8 := $(VENV)/bin/flake8
BLACK := $(VENV)/bin/black
# Use golangci-lint from PATH if available, otherwise use the one installed by src/go
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2> /dev/null || echo $(abspath ../src/go/bin/golangci-lint))

all: format lint test run

# Define a helper for checking command existence
check-command = @if ! command -v $(1) > /dev/null; then \
		echo "Error: '$(1)' not found. $(2)"; \
		exit 1; \
	fi

help:
	@echo "Available targets:"
	@echo ""
	@echo "Development:"
	@echo "  all            - Run all tools (format, lint, test, run)"
	@echo "  check          - Run linters without fixing"
	@echo "  format         - Format code (go fmt, black)"
	@echo "  lint           - Run linters and fix issues"
	@echo "  test           - Run tests for all examples"
	@echo ""
	@echo "Installation:"
	@echo "  install-dev    - Install development dependencies"
	@echo ""
	@echo "Examples:"
	@echo "  example-go     - Build and test Go example"
	@echo "  example-python - Build and test Python example"
	@echo "  run-go         - Run Go example with dummy input"
	@echo "  run-python     - Run Python example with dummy input"
	@echo "  run            - Run both Go and Python examples"
	@echo ""
	@echo "Cleanup:"
	@echo "  clean          - Clean build artifacts"
	@echo "  clean-venv     - Remove the virtual environment"
	@echo ""
	@echo "Help:"
	@echo "  help           - Show this help message"

build: example-go example-python

# Ensure venv exists and is up to date
$(VENV)/bin/activate: python/requirements.txt
	$(call check-command,$(SYSTEM_PYTHON),Please install Python 3.12+ or set SYSTEM_PYTHON variable)
	@echo "Setting up virtual environment..."
	@if ! $(SYSTEM_PYTHON) -c "import ensurepip" > /dev/null 2>&1; then \
		echo "Error: Python 'venv' module is not available. On Debian/Ubuntu, please run: sudo apt install python3-venv"; \
		exit 1; \
	fi
	$(SYSTEM_PYTHON) -m venv $(VENV)
	$(PIP) install -r python/requirements.txt
	touch $(VENV)/bin/activate

example-go:
	@echo "Building go example"
	$(call check-command,$(GO),Please install Go 1.24+ or set GO variable)
	cd go && $(GO) mod tidy
	cd go && $(GO) build -v -o example-app .
	cd go && $(GO) test -v .

example-python: $(VENV)/bin/activate
	@echo "Building python example"
	$(PYTHON) -m py_compile python/app.py
	PYTHONPATH=python $(PYTHON) -m pytest python/test_app.py

run-go: example-go
	@echo ""
	@echo "Running go example with dummy input..."
	@echo "----------------------------------------------------------------"
	@echo "NOTE: The Go app adds an 'example-go-processed' annotation to the JSON on STDOUT."
	@echo "All log messages are written to STDERR."
	@echo "----------------------------------------------------------------"
	@echo '{"apiVersion": "phenix.sandia.gov/v1", "kind": "Experiment", "spec": {"experimentName": "manual-run", "baseDir": "/tmp/manual-run", "scenario": {"apps": [{"name": "example", "hosts": []}]}}}' | ./go/example-app running
	@echo ""

run: run-go run-python

run-python: $(VENV)/bin/activate
	@echo ""
	@echo "Running python example with dummy input..."
	@echo "----------------------------------------------------------------"
	@echo "NOTE: An ERROR log is expected below; this demonstrates structured error logging."
	@echo "The Python app does NOT modify the JSON on STDOUT in the 'running' stage."
	@echo "All log messages are written to STDERR."
	@echo "----------------------------------------------------------------"
	@echo '{"apiVersion": "phenix.sandia.gov/v1", "kind": "Experiment", "spec": {"experimentName": "manual-run", "baseDir": "/tmp/manual-run", "scenario": {"apps": [{"name": "example", "hosts": []}]}}}' | PHENIX_LOG_FILE=stderr $(PYTHON) python/app.py running
	@echo ""

clean: clean-venv
	$(RM) go/example-app
	$(RM) -r python/__pycache__
	$(RM) -r python/.pytest_cache

clean-venv:
	$(RM) -r $(VENV)

install-dev: $(VENV)/bin/activate

check: $(VENV)/bin/activate
	@echo "Checking go example..."
	$(call check-command,$(GOLANGCI_LINT),Please run 'make install-dev' from the root directory.)
	cd go && $(GOLANGCI_LINT) run --config ../../src/go/.golangci.yml
	@echo "Checking python example..."
	$(FLAKE8) python/

format: $(VENV)/bin/activate
	@echo "Formatting go example..."
	$(call check-command,$(GO),Please install Go 1.24+ or set GO variable)
	cd go && $(GO) fmt ./...
	@echo "Formatting python example..."
	$(BLACK) python/

lint: $(VENV)/bin/activate
	@echo "Linting go example..."
	$(call check-command,$(GOLANGCI_LINT),Please run 'make install-dev' from the root directory.)
	cd go && $(GOLANGCI_LINT) run --config ../../src/go/.golangci.yml --fix
	@echo "Linting python example..."
	$(FLAKE8) python/

test: example-go example-python
