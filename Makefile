.PHONY: all build clean test install help docker-ssh-image test-ssh test-ssh-short deps fmt lint lint-ci check

# Binary output directory
BIN_DIR := ./bin
BINARY_NAME := mup

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := gotestsum --
GOMOD := $(GOCMD) mod
GOINSTALL := $(GOCMD) install

# Build flags
LDFLAGS := -ldflags "-s -w"

all: help

## build: Build the mup binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/mup
	@echo "✓ Built $(BIN_DIR)/$(BINARY_NAME)"

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BIN_DIR)
	@rm -rf test/bin/$(BINARY_NAME)
	@echo "✓ Cleaned"

## test: Run tests (excluding integration tests that download binaries)
test:
	@echo "Running tests..."
	$(GOTEST) -short -v ./...

## test-short: Alias for test (runs quick unit tests)
test-short:
	@echo "Running quick tests..."
	$(GOTEST) -short -v ./...

## test-integration: Run all integration tests (including binary downloads)
test-integration:
	@echo "Running integration tests (this may download binaries)..."
	$(GOTEST) -v ./...

## test-coverage: Run tests with coverage (excluding integration tests)
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -short -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report generated: coverage.html"

## test-ssh-short: Run quick SSH tests (skip integration tests)
test-ssh-short:
	@echo "Running quick SSH tests..."
	$(GOTEST) -short -v ./pkg/executor/

## test-ssh: Run full SSH integration tests (requires Docker)
test-ssh: docker-ssh-image
	@echo "Running SSH integration tests..."
	@echo "Note: This will launch Docker containers for testing"
	$(GOTEST) -v -run TestSSH ./pkg/executor/

## test-all: Run all tests including SSH integration tests
test-all: docker-ssh-image
	@echo "Running all tests including SSH integration tests..."
	$(GOTEST) -v ./...

## test-e2e: Run end-to-end tests against actual binary
test-e2e: build
	@echo "Running end-to-end tests..."
	@echo "Building test binary to test/bin/mup..."
	@mkdir -p test/bin
	$(GOBUILD) -o test/bin/$(BINARY_NAME) ./cmd/mup
	@echo "Running E2E tests..."
	$(GOTEST) -v -tags=e2e ./test/e2e/...
	@echo "✓ E2E tests complete"

## test-e2e-verbose: Run E2E tests with verbose output
test-e2e-verbose: build
	@echo "Running end-to-end tests (verbose)..."
	@mkdir -p test/bin
	$(GOBUILD) -o test/bin/$(BINARY_NAME) ./cmd/mup
	$(GOTEST) -v -tags=e2e ./test/e2e/... -test.v
	@echo "✓ E2E tests complete"

## test-complete: Run all test suites (unit, integration, e2e, ssh)
test-complete: test-integration test-e2e test-ssh
	@echo "✓ All test suites passed"

## install: Install mup to $GOPATH/bin
install:
	@echo "Installing $(BINARY_NAME)..."
	$(GOINSTALL) ./cmd/mup
	@echo "✓ Installed $(BINARY_NAME)"

## deps: Download and tidy dependencies, install development tools
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "✓ Dependencies updated"
	@echo ""
	@echo "Installing development tools..."
	@echo "Installing golangci-lint..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "Installing gotestsum..."
	@go install gotest.tools/gotestsum@latest
	@echo "✓ Development tools installed"
	@echo ""
	@echo "Verify installation:"
	@golangci-lint --version || echo "  ✗ golangci-lint not found"
	@gotestsum --version || echo "  ✗ gotestsum not found"

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GOCMD) fmt ./...
	@echo "✓ Code formatted"

## lint: Run golangci-lint (includes go vet and errcheck)
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
		echo "✓ Lint complete"; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

## lint-ci: Run linter in CI mode (fails on errors, no install check)
lint-ci:
	@echo "Running linter (CI mode)..."
	golangci-lint run ./...
	@echo "✓ Lint complete"

## check: Run all checks (lint includes vet and errcheck)
check: lint
	@echo "✓ All checks passed"

## run: Build and run playground start command
run: build
	$(BIN_DIR)/$(BINARY_NAME) playground start

## docker-ssh-image: Build Docker image for SSH testing
docker-ssh-image:
	@echo "Building SSH test node Docker image..."
	@bash test/docker/ssh-node/build.sh

## help: Show this help message
help:
	@echo "Mup - MongoDB Cluster Management Tool"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/  /'
