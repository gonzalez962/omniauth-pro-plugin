# Makefile for OmniAuth Pro Plugin (CPAMC)

BINARY_NAME ?= omni-auth-pro-linux-amd64.so
PLUGIN_DIR ?= $(HOME)/services/cliproxy/plugins

.PHONY: all build clean install test

all: build

## Run unit tests
test:
	@echo "==> Running tests..."
	go test -v ./...

## Build the shared C library (.so) with optimizations
build:
	@echo "==> Building $(BINARY_NAME)..."
	CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -buildmode=c-shared -o $(BINARY_NAME) .
	@echo "==> Successfully compiled $(BINARY_NAME)"

## Install the plugin into the CPAMC plugin directory
install: build
	@echo "==> Installing plugin to $(PLUGIN_DIR)..."
	@mkdir -p $(PLUGIN_DIR)
	@cp $(BINARY_NAME) $(PLUGIN_DIR)/
	@echo "==> Plugin installed to $(PLUGIN_DIR)/$(BINARY_NAME)"

## Clean generated artifacts
clean:
	@echo "==> Cleaning build artifacts..."
	@rm -f $(BINARY_NAME) $(BINARY_NAME:.so=.h)
	@echo "==> Clean complete."
