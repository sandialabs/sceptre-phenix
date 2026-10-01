.PHONY: all clean dist help install install-dev
.DEFAULT_GOAL := help

SHELL := /bin/bash

# Everything the build reads: the app's source and static files, the page,
# Vite's config and plugins, its environment and the locked dependencies. A
# change to any of them rebuilds dist/, including the Builder v2 file list and
# compressed copies plugins/builder-v2-assets.js writes for the server.
SOURCES := $(shell find ./src ./public ./plugins -type f) \
	index.html vite.config.js package.json package-lock.json $(wildcard .env .env.*)

# Define a helper for checking command existence
check-command = @if ! command -v $(1) > /dev/null; then \
		echo "Error: '$(1)' not found. $(2)"; \
		exit 1; \
	fi

help:
	@echo "Available targets:"
	@echo ""
	@echo "Build:"
	@echo "  all     - Build the frontend application"
	@echo "  dist    - Build the distribution files"
	@echo ""
	@echo "Cleanup:"
	@echo "  clean   - Remove build artifacts and node_modules"
	@echo ""
	@echo "Installation:"
	@echo "  install - Install dependencies"
	@echo "  install-dev - Install development dependencies"
	@echo ""
	@echo "Help:"
	@echo "  help    - Show this help message"
	@echo ""
	@echo "NOTE: Linting/formatting (ESLint, Prettier) is handled by 'prek'"
	@echo "      from the repository root, not by this Makefile. Run 'make"
	@echo "      lint' (or 'prek run --all-files') from the repo root."

all: dist

clean:
	$(RM) -r dist
	$(RM) -r node_modules

install:
	$(call check-command,npm,Please install Node.js and npm (https://nodejs.org))
	npm ci

install-dev: install

dist: dist/index.html

dist/index.html: $(SOURCES)
	$(call check-command,npm,Please install Node.js and npm (https://nodejs.org))
	npm ci && npm run build
