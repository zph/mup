.PHONY: all build clean test install help

# Binary output directory
BIN_DIR := ./bin
BINARY_NAME := mup

# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOMOD := $(GOCMD) mod
GOINSTALL := $(GOCMD) install

# Build flags
LDFLAGS := -ldflags "-s -w"

all: build

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

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

## test-coverage: Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report generated: coverage.html"

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

## help: Show this help message
help:
	@echo "Mup - MongoDB Cluster Management Tool"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/  /'
