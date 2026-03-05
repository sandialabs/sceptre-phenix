.PHONY: all clean dist help install install-dev
.DEFAULT_GOAL := help

SHELL := /bin/bash

SOURCES := $(shell find ./src \( -name '*.js' -o -name '*.vue' \))

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

all: dist

clean:
	$(RM) -r dist
	$(RM) -r node_modules

install:
	$(call check-command,yarn,Please install yarn (e.g. sudo npm install -g yarn))
	yarn install

install-dev: install

dist: dist/index.html

dist/index.html: $(SOURCES)
	$(call check-command,yarn,Please install yarn (e.g. sudo npm install -g yarn))
	yarn install
	yarn build
