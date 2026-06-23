BINARY_NAME=ledger

all: fmt lint test build vuln

.PHONY: fmt lint vuln build test

# Format code and imports
fmt:
	go tool goimports -w .
	go fmt ./...

# Run code style and syntax checks
lint:
	golangci-lint run

# Scan for known vulnerabilities
vuln: build
	go tool govulncheck -mode binary bin/$(BINARY_NAME)

# Compile the binary
build:
	go build -o bin/$(BINARY_NAME) main.go

# Run tests
test:
	go test -v -race ./...

