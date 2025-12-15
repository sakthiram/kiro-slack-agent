.PHONY: build test lint run clean deps mocks test-integration test-e2e

# Build the server binary
build:
	go build -o bin/server ./cmd/server

# Run all tests with coverage
test:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run linter
lint:
	golangci-lint run ./...

# Run the server
run:
	go run ./cmd/server

# Clean build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html

# Download dependencies
deps:
	go mod download
	go mod tidy

# Generate mocks
mocks:
	go generate ./...

# Integration tests (requires kiro-cli)
test-integration:
	go test -v -tags=integration ./...

# E2E tests (requires Slack test workspace)
test-e2e:
	E2E_TEST=true go test -v -tags=e2e ./internal/e2e/...
