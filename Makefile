.PHONY: all build clean test install help docker-ssh-image test-ssh test-ssh-short

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

## install: Install mup to $GOPATH/bin
install:
	@echo "Installing $(BINARY_NAME)..."
	$(GOINSTALL) ./cmd/mup
	@echo "✓ Installed $(BINARY_NAME)"

## deps: Download and tidy dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "✓ Dependencies updated"

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GOCMD) fmt ./...
	@echo "✓ Code formatted"

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GOCMD) vet ./...
	@echo "✓ Vet complete"

## lint: Run golangci-lint (requires golangci-lint installed)
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
		echo "✓ Lint complete"; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

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
