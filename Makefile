.PHONY: build build-dev install clean test lint fmt tidy help build-linux build-windows build-darwin run

BINARY_NAME := syncer
BUILD_DIR := bin
MAIN_PATH := ./cmd/syncer
VERSION ?= 

GO_BUILD_FLAGS := -trimpath
GO_LDFLAGS := -s -w

ifneq ($(VERSION),)
	GO_LDFLAGS += -X main.version=$(VERSION)
endif

default: build

build:
	@echo "Building $(BINARY_NAME) (production-optimized)..."
	@mkdir -p $(BUILD_DIR)
	@go build $(GO_BUILD_FLAGS) -ldflags '$(GO_LDFLAGS)' -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

build-dev:
	@echo "Building $(BINARY_NAME) (development)..."
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

install:
	@echo "Installing $(BINARY_NAME)..."
	@go install $(GO_BUILD_FLAGS) -ldflags '$(GO_LDFLAGS)' $(MAIN_PATH)
	@echo "Installation complete"

clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

test:
	@echo "Running tests..."
	@go test ./...

lint:
	@echo "Running linter..."
	@golangci-lint run

fmt:
	@echo "Formatting code..."
	@gofmt -s -w .

tidy:
	@echo "Tidying modules..."
	@go mod tidy

build-linux:
	@echo "Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags '$(GO_LDFLAGS)' -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64"

build-windows:
	@echo "Building $(BINARY_NAME) for Windows..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=windows GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags '$(GO_LDFLAGS)' -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe"

build-darwin:
	@echo "Building $(BINARY_NAME) for macOS..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=darwin GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags '$(GO_LDFLAGS)' -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64"

run:
	@bash -c '\
	if [ ! -f .env ]; then \
		echo "Error: .env file not found"; \
		exit 1; \
	fi; \
	export $$(grep -v "^#" .env | xargs); \
	if [ -z "$$SPOTIFY_CLIENT_ID" ] || [ "$$SPOTIFY_CLIENT_ID" = "id" ]; then \
		echo "Error: Please update .env file with your actual SPOTIFY_CLIENT_ID"; \
		exit 1; \
	fi; \
	if [ -z "$$SPOTIFY_CLIENT_SECRET" ] || [ "$$SPOTIFY_CLIENT_SECRET" = "secret" ]; then \
		echo "Error: Please update .env file with your actual SPOTIFY_CLIENT_SECRET"; \
		exit 1; \
	fi; \
	if [ ! -f $(BUILD_DIR)/$(BINARY_NAME) ]; then \
		echo "Building syncer..."; \
		$(MAKE) build; \
	fi; \
	exec $(BUILD_DIR)/$(BINARY_NAME) $(ARGS)'

help:
	@echo "Available targets:"
	@echo "  build          - Production-optimized build (default)"
	@echo "  build-dev      - Development build (no optimizations)"
	@echo "  run            - Load .env, build if needed, and run syncer"
	@echo "  install        - Install binary to GOPATH/bin or GOBIN"
	@echo "  clean          - Remove build artifacts"
	@echo "  test           - Run tests"
	@echo "  lint           - Run golangci-lint"
	@echo "  fmt            - Format code with gofmt"
	@echo "  tidy           - Run go mod tidy"
	@echo "  build-linux    - Build for Linux (amd64)"
	@echo "  build-windows  - Build for Windows (amd64)"
	@echo "  build-darwin   - Build for macOS (amd64)"
	@echo "  help           - Show this help message"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION        - Optional version string (e.g., make build VERSION=v1.0.0)"
	@echo "  ARGS           - Arguments to pass to syncer (e.g., make run ARGS=\"auth\" or make run ARGS=\"run --mode fast\")"
