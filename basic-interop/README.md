# Basic Interop Tests

This folder contains integration tests for Ethereum client interoperability using Kurtosis.

## Prerequisites

- Go 1.22 or later
- Kurtosis CLI installed
- Docker running

## Test Configuration

The test configuration is defined in `input_args.yaml`. You can modify this file to test different client combinations:

```yaml
participants:
  - el_client_type: geth
    cl_client_type: lighthouse
  - el_client_type: nethermind
    cl_client_type: teku
  - el_client_type: besu
    cl_client_type: prysm
additional_services:
  - spamoor
```

## Running Tests

1. Run all tests:
```bash
go test -v -timeout 1h ./...
```

2. Run a specific test:
```bash
go test -v -timeout 1h basic-interop/main_test.go
```

3. Run with verbose logging:
```bash
go test -v -count=1 -timeout 1h basic-interop/main_test.go
```

## Test Behavior

The test will:
1. Create a Kurtosis enclave
2. Deploy the specified Ethereum clients
3. Wait for finalization (5 epochs)
4. Verify all nodes are synced
5. Clean up the enclave

