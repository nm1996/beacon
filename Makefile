
.PHONY: run build fmt vet lint lint-full test check clean

APP_NAME := beacon
BUILD_DIR := bin

# Run application
run:
	go run ./cmd/beacon

# Build binary
build:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/beacon

# Format all Go files
fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

# Built-in static analysis
vet:
	go vet ./...

# Lint (works without external tools)
lint: vet
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed; ran go vet only."; \
	fi

# Full lint
lint-full:
	golangci-lint run ./...

# Run tests
test:
	go test -race ./...

# Format + lint + tests
check: fmt lint test

# Remove build artifacts
clean:
	rm -rf $(BUILD_DIR)
