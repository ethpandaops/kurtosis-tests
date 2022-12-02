Ethereum Network Partitioning Example
=====================================
A test that runs a mini testnet inside kurtosis, and deploys a contract with an `EXTCODECOPY` in its init code.

NOTE 2: The ethereum network used is started port-merge.

### Demo Flow
1. Make sure you have a Kurtosis engine running
1. Run the Go test inside of `main_test.go` (can be done with Goland)
1. Notice how:
    - A new Kurtosis enclave is created (run `kurtosis enclave ls`)
    - Ethereum containers get started inside Docker
    - The Ethereum nodes are in agreement on the block tip hash:
      ```
      ethereum-node-0          block number: 6, block hash: 0x856d09c24d8cc8a2fed87b961c5084b4f1bc9857654a8bd186d6fc74a6c90ab3
      ethereum-node-1          block number: 6, block hash: 0x856d09c24d8cc8a2fed87b961c5084b4f1bc9857654a8bd186d6fc74a6c90ab3
      ...
      ethereum-node-N          block number: 6, block hash: 0x856d09c24d8cc8a2fed87b961c5084b4f1bc9857654a8bd186d6fc74a6c90ab3
      ```
1. Shell into the `ethereum-node-0` (can be done with `kurtosis service shell`)
1. On the `ethereum-node-0`, run the following to have `ethereum-node-0` start pinging the last node `ethereum-node-N` (`NODENUMBER` in the command below is the total number of nodes in the network minus 1):
   ```
   apk update && apk add curl && while true; do curl --connect-timeout 3 -XGET -H "content-type: application/json" "http://ethereum-node-${NODENUMBER}:8545" --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'; sleep 1; done
   ```
1. The test will then deploy a contract with `EXTCODECOPY` and finally wait for a certain duration
2. Notice how:
    - All the nodes agree once more on the block hash (will take a little bit):
      ```
      ethereum-node-0          block number: 23, block hash: 0x4e44e9ce3aadff408e1758d3e53250b85a28b83fa8d722a4826c056407301957
      ethereum-node-1          block number: 23, block hash: 0x4e44e9ce3aadff408e1758d3e53250b85a28b83fa8d722a4826c056407301957
      ethereum-node-2          block number: 23, block hash: 0x4e44e9ce3aadff408e1758d3e53250b85a28b83fa8d722a4826c056407301957
      ```
    - The `curl` command on `ethereum-node-0` returns to being able to reach `ethereum-node-N`:
      ```
      curl: (28) Connection timeout after 3002 ms
      curl: (28) Connection timeout after 3000 ms
      curl: (28) Connection timeout after 3000 ms
      {"jsonrpc":"2.0","id":1,"result":"0x14"}
      {"jsonrpc":"2.0","id":1,"result":"0x14"}
      {"jsonrpc":"2.0","id":1,"result":"0x14"}
      ```
