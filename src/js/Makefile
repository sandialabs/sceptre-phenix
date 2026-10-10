.PHONY: all clean dist help install install-dev ui FORCE
.DEFAULT_GOAL := help

SHELL := /bin/bash

# Everything the build reads: the app's source and static files, the page,
# Vite's config and plugins, its environment and the locked dependencies. A
# change to any of them rebuilds dist/, including the Builder file list and
# compressed copies plugins/builder-assets.js writes for the server.
SOURCES := $(shell find ./src ./public ./plugins -type f) \
	index.html vite.config.js package.json package-lock.json $(wildcard .env .env.*)

# The Brotli quality plugins/builder-assets.js compresses Builder's files
# with: PHENIX_BROTLI_QUALITY, or 9 when it is not set. The build records it
# in dist/.brotli-quality, and a build that asks for another quality than the
# recorded one rebuilds dist/. The copies of dist/* into src/go/web/public
# leave the dotfile out.
BROTLI_QUALITY := $(if $(PHENIX_BROTLI_QUALITY),$(PHENIX_BROTLI_QUALITY),9)
BROTLI_QUALITY_RECORD := dist/.brotli-quality

ifneq ($(BROTLI_QUALITY),$(shell cat $(BROTLI_QUALITY_RECORD) 2>/dev/null))
dist/index.html: FORCE
endif

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
	@echo "  ui      - Same as dist"
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

ui: dist

dist/index.html: $(SOURCES)
	$(call check-command,npm,Please install Node.js and npm (https://nodejs.org))
	npm ci && npm run build
	echo '$(BROTLI_QUALITY)' > $(BROTLI_QUALITY_RECORD)

FORCE:
