# Kurtosis Tests

This repository contains integration tests for Ethereum client interoperability using Kurtosis with go. 

## Test Suites

### Basic Interop Tests
Tests basic interoperability between different Ethereum clients. Verifies finalization and syncing.
- [View Tests](basic-interop/README.md)


## Prerequisites

- Go 1.22 or later
- Kurtosis CLI installed
- Docker running

## Running Tests

Each test suite has its own README with specific instructions. Generally:

```bash
# Run all tests
go test -v -timeout 1h ./...

# Run specific test suite
go test -v -timeout 1h ./basic-interop/...
```
