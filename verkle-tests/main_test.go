package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/params"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/core/lib/enclaves"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/core/lib/services"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/engine/lib/kurtosis_context"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/stretchr/testify/require"
)

/*
This example will:
1. Start an Ethereum network with `numParticipants` nodes
2. Wait for all the nodes to be synced
3. Deploy a smart contract
4. Wait for the block production to continue
5. Assert that there was no forking

This test demonstrates basic Ethereum testnet behaviour in Kurtosis.
*/

const (
	testName              = "go-ethereum-testnet-with-contract"
	isPartitioningEnabled = true

	nodeInfoPrefix = "NODES STATUS -- |"

	eth2StarlarkPackage = "github.com/kurtosis-tech/eth2-package"

	// must be something greater than 4 to have at least 2 nodes in each partition
	numParticipants = 4

	participantsPlaceholder = "{{participants_param}}"
	//TODO: Replace with image pulled from commit ref
	//participantParam        = `{"elType":"geth","elImage":"ethereum/client-go:v1.10.25","clType":"lodestar","clImage":"chainsafe/lodestar:v1.1.0"}`
	participantParam = `{"el_client_type":"geth","el_client_image":"parithoshj/geth:fix-beverly-hills-v0.2-c65f6b0","cl_client_type":"lighthouse","cl_client_image":"sigp/lighthouse:v3.3.0"}`
	// Sets parameters to run the kurtosis module with
	// launch_additional_services decides if grafana, forkmon and other additional servies are launched
	// participants sets the included clients
	// network_params sets the networkID and mnemonic. These need to be set to ensure the private key used for signing the
	// tx is well funded.
	moduleParamsTemplate = `{
	"launch_additional_services": false,
	"participants": [
		` + participantsPlaceholder + `
	],
	"network_params": [
		{	"network_id": "3151908", 
			"preregistered_validator_keys_mnemonic": "giant issue aisle success illegal bike spike question tent bar rely arctic volcano long crawl hungry vocal artwork sniff fantasy very lucky have athlete"
		}
	]
}`

	minBlocksBeforeDeployment = 5
	minBlocksAfterDeployment  = 10

	elNodeIdTemplate          = "el-client-%d"
	clNodeBeaconIdTemplate    = "cl-client-%d-beacon"
	clNodeValidatorIdTemplate = "cl-client-%d-validator"

	rpcPortId = "rpc"

	retriesAttempts      = 20
	retriesSleepDuration = 10 * time.Millisecond
)

var (
	nodeIds    = make([]int, numParticipants)
	idsToQuery = make([]services.ServiceID, numParticipants)

	isTestInExecution bool

	// Deployment code for a contract that calls EXTCOPY during contract initialization.
	// source for this contract can be found at https://gist.github.com/gballet/a23db1e1cb4ed105616b5920feb75985
	contractCode = common.Hex2Bytes("60806040526040516100109061017b565b604051809103906000f08015801561002c573d6000803e3d6000fd5b506000806101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555034801561007857600080fd5b5060008067ffffffffffffffff8111156100955761009461024a565b5b6040519080825280601f01601f1916602001820160405280156100c75781602001600182028036833780820191505090505b50905060008060009054906101000a900473ffffffffffffffffffffffffffffffffffffffff1690506020600083833c81610101906101e3565b60405161010d90610187565b61011791906101a3565b604051809103906000f080158015610133573d6000803e3d6000fd5b50600160006101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff160217905550505061029b565b60d58061046783390190565b6102068061053c83390190565b61019d816101d9565b82525050565b60006020820190506101b86000830184610194565b92915050565b6000819050602082019050919050565b600081519050919050565b6000819050919050565b60006101ee826101ce565b826101f8846101be565b905061020381610279565b925060208210156102435761023e7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff8360200360080261028e565b831692505b5050919050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052604160045260246000fd5b600061028582516101d9565b80915050919050565b600082821b905092915050565b6101bd806102aa6000396000f3fe608060405234801561001057600080fd5b506004361061002b5760003560e01c8063f566852414610030575b600080fd5b61003861004e565b6040516100459190610146565b60405180910390f35b6000600160009054906101000a900473ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff166381ca91d36040518163ffffffff1660e01b815260040160206040518083038186803b1580156100b857600080fd5b505afa1580156100cc573d6000803e3d6000fd5b505050506040513d601f19601f820116820180604052508101906100f0919061010a565b905090565b60008151905061010481610170565b92915050565b6000602082840312156101205761011f61016b565b5b600061012e848285016100f5565b91505092915050565b61014081610161565b82525050565b600060208201905061015b6000830184610137565b92915050565b6000819050919050565b600080fd5b61017981610161565b811461018457600080fd5b5056fea2646970667358221220065863d77ef2920706d664472b62e5c41f2da9e415f58493964dc51a710a937564736f6c63430008070033608060405234801561001057600080fd5b5060b68061001f6000396000f3fe6080604052348015600f57600080fd5b506004361060285760003560e01c8063ab5ed15014602d575b600080fd5b60336047565b604051603e9190605d565b60405180910390f35b60006001905090565b6057816076565b82525050565b6000602082019050607060008301846050565b92915050565b600081905091905056fea2646970667358221220df91a8df66c4433e0b980f71bc0fd7987cf274d28fa5175d9c60e136f4bef03264736f6c63430008070033608060405234801561001057600080fd5b5060405161020638038061020683398181016040528101906100329190610063565b60018160001c6100429190610090565b60008190555050610145565b60008151905061005d8161012e565b92915050565b60006020828403121561007957610078610129565b5b60006100878482850161004e565b91505092915050565b600061009b826100f0565b91506100a6836100f0565b9250827fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff038211156100db576100da6100fa565b5b828201905092915050565b6000819050919050565b6000819050919050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052601160045260246000fd5b600080fd5b610137816100e6565b811461014257600080fd5b50565b60b3806101536000396000f3fe6080604052348015600f57600080fd5b506004361060285760003560e01c806381ca91d314602d575b600080fd5b60336047565b604051603e9190605a565b60405180910390f35b60005481565b6054816073565b82525050565b6000602082019050606d6000830184604d565b92915050565b600081905091905056fea26469706673582212209b386e1a4e0efdcce8424e6e058b1e781a1377acbc48c3d3f2db43b08021ef9164736f6c63430008070033")

	config = &params.ChainConfig{
		ChainID:             big.NewInt(3151908),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		MuirGlacierBlock:    big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		Ethash:              new(params.EthashConfig),
		CancunBlock:         big.NewInt(0),
	}
	signer     = types.LatestSigner(config)
	testkey, _ = crypto.HexToECDSA("ef5177cd0b6b21c87db5a0bf35d4084a8a57a9d6a064f86d51ac85f2b873a4e2")
	from       = crypto.PubkeyToAddress(testkey.PublicKey)
)

func TestExtCopyInContractDeployment(t *testing.T) {
	isTestInExecution = true
	moduleParams := initNodeIdsAndRenderModuleParam()

	ctx := context.Background()

	log.Printf("------------ CONNECTING TO KURTOSIS ENGINE ---------------")
	kurtosisCtx, err := kurtosis_context.NewKurtosisContextFromLocalEngine()
	require.NoError(t, err, "An error occurred connecting to the Kurtosis engine")

	enclaveId := enclaves.EnclaveID(fmt.Sprintf(
		"%v-%v",
		testName, time.Now().Unix(),
	))
	enclaveCtx, err := kurtosisCtx.CreateEnclave(ctx, enclaveId, isPartitioningEnabled)
	require.NoError(t, err, "An error occurred creating the enclave")
	defer func() {
		if !isTestInExecution {
			_ = kurtosisCtx.DestroyEnclave(ctx, enclaveId)
			_, _ = kurtosisCtx.Clean(ctx, false)
		}
	}()

	log.Printf("------------ EXECUTING MODULE ---------------")
	starlarkRunResult, err := enclaveCtx.RunStarlarkRemotePackageBlocking(ctx, eth2StarlarkPackage, moduleParams, false)
	require.NoError(t, err, "An error executing loading the ETH module")
	require.Nil(t, starlarkRunResult.InterpretationError)
	require.Empty(t, starlarkRunResult.ValidationErrors)
	require.Nil(t, starlarkRunResult.ExecutionError)

	nodeClientsByServiceIds, err := getElNodeClientsByServiceID(enclaveCtx, idsToQuery)
	require.NoError(t, err, "An error occurred when trying to get the node clients for services with IDs '%+v'", idsToQuery)

	log.Printf("------------ STARTING TEST CASE ---------------")
	stopPrintingFunc, err := printNodeInfoUntilStopped(
		ctx,
		nodeClientsByServiceIds,
	)
	require.NoError(t, err, "An error occurred launching the node info printer thread")
	defer stopPrintingFunc()

	log.Printf("------------ CHECKING ALL NODES ARE IN SYNC AT BLOCK '%d' ---------------", minBlocksBeforeDeployment)
	syncedBlockNumber, err := waitUntilAllNodesGetSynced(ctx, idsToQuery, nodeClientsByServiceIds, minBlocksBeforeDeployment)
	require.NoError(t, err, "An error occurred waiting until all nodes get synced before inducing the partition")
	log.Printf("------------ ALL NODES SYNCED AT BLOCK NUMBER '%v' ------------", syncedBlockNumber)
	printAllNodesInfo(ctx, nodeClientsByServiceIds)
	log.Printf("------------ VERIFIED ALL NODES ARE IN SYNC BEFORE SENDING THE TX ------------")

	log.Printf("------------ SENDING THE CONTRACT DEPLOYMENT TX ------------")
	client := nodeClientsByServiceIds["el-client-0"]
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		t.Fatal("could not get nonce")
	}
	tx, _ := types.SignTx(types.NewContractCreation(nonce, big.NewInt(0), 600000, big.NewInt(875000000), contractCode), signer, testkey)
	err = client.SendTransaction(ctx, tx)
	if err != nil {
		t.Fatalf("error sending the contract transaction: %v", err)
	}

	log.Printf("------------ CHECKING ALL NODES ARE STILL IN SYNC AT BLOCK '%d' ---------------", minBlocksBeforeDeployment+minBlocksAfterDeployment)
	syncedBlockNumber, err = waitUntilAllNodesGetSynced(ctx, idsToQuery, nodeClientsByServiceIds, minBlocksBeforeDeployment+minBlocksAfterDeployment)
	require.NoError(t, err, "An error occurred waiting until all nodes get synced after inducing the partition")
	log.Printf("----------- ALL NODES SYNCED AT BLOCK NUMBER '%v' -----------", syncedBlockNumber)
	printAllNodesInfo(ctx, nodeClientsByServiceIds)
	log.Printf("----------- VERIFIED THAT ALL NODES ARE IN SYNC AFTER DEPLOYING CONTRACT --------------")

	// sanity check: ensure that the tx has been "mined"
	var contractaddr common.Address
	var receipt *types.Receipt
	for {
		time.Sleep(time.Second * 5)
		receipt, err = client.TransactionReceipt(ctx, tx.Hash())
		if err == nil {
			contractaddr = receipt.ContractAddress
			break
		}
	}

	log.Printf("contract address=%x %v", contractaddr, err)

	// from := common.HexToAddress("0xAb2A01BC351770D09611Ac80f1DE076D56E0487d")
	log.Printf("reading code %x %x", contractaddr, from)
	if code, err := client.PendingCodeAt(ctx, contractaddr); len(code) == 0 || err != nil {
		t.Fatalf("could not get code code=%x err=%v", code, err)
	}
	log.Printf("----------- VERIFIED THAT THE CONTRACT DEPLOYMENT TX HAS BEEN INCLUDED -------------")
	blocknr, err := client.BlockNumber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	log.Printf("------------ CHECKING THE EXTCOPY WORKED AT BLOCK '%d' %x %x ---------------", blocknr, contractaddr, from)
	receivedContractData, err := client.PendingCallContract(ctx, ethereum.CallMsg{
		From: from,
		To:   &contractaddr,
		Data: common.FromHex("0xf5668524"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var expectedContractData = common.FromHex("80604052348015600f57600080fd5b506004361060285760003560e01c8063ac")

	err = compareContractData(expectedContractData, receivedContractData)
	require.NoError(t, err, "Contract deployment was unsuccessful! ")

	log.Printf("----------- VERIFIED THAT CONTRACT DEPLOYMENT PRODUCED THE CORRECT OUTPUT  --------------")

	// Test teardown phase
	t.Skip("let's see if the first test causes the second one to fail")
	isTestInExecution = false
	log.Printf("------------ TEST FINISHED ---------------")
}

// TestReadGenesisTree tests for a bug that was found in Beverly Hills v0.2:
// the tree that was stored at genesis time was incorrect because the buffer
// that was used for storing values in the database was reused, and so a single
// value was inserted many times over. This bug was uncovered when the deposit
// contract was added to the testnet: the initial deposits were all given to
// the same address although this isn't what caused the network split: because
// the value that would be used to compute the commitment was dependent on a
// map enumeration order, nodes disagreed on the commitment for that leaf node
// and would report invalid blocks.
func TestReadGenesisTree(t *testing.T) {
	isTestInExecution = true
	moduleParams := initNodeIdsAndRenderModuleParam()

	ctx := context.Background()

	log.Printf("------------ CONNECTING TO KURTOSIS ENGINE ---------------")
	kurtosisCtx, err := kurtosis_context.NewKurtosisContextFromLocalEngine()
	require.NoError(t, err, "An error occurred connecting to the Kurtosis engine")

	enclaveId := enclaves.EnclaveID(fmt.Sprintf(
		"%v-%v",
		testName, time.Now().Unix(),
	))
	enclaves, err := kurtosisCtx.GetEnclaves(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(enclaves)

	enclaveCtx, err := kurtosisCtx.CreateEnclave(ctx, enclaveId, false)
	require.NoError(t, err, "An error occurred creating the enclave")
	defer func() {
		if !isTestInExecution {
			_ = kurtosisCtx.DestroyEnclave(ctx, enclaveId)
			_, _ = kurtosisCtx.Clean(ctx, false)
		}
	}()

	log.Printf("------------ EXECUTING MODULE ---------------")
	starlarkRunResult, err := enclaveCtx.RunStarlarkRemotePackageBlocking(ctx, eth2StarlarkPackage, moduleParams, false)
	t.Log(err)
	require.NoError(t, err, "An error executing loading the ETH module")
	require.Nil(t, starlarkRunResult.InterpretationError)
	require.Empty(t, starlarkRunResult.ValidationErrors)
	require.Nil(t, starlarkRunResult.ExecutionError)

	nodeClientsByServiceIds, err := getElNodeClientsByServiceID(enclaveCtx, idsToQuery)
	require.NoError(t, err, "An error occurred when trying to get the node clients for services with IDs '%+v'", idsToQuery)

	log.Printf("------------ STARTING TEST CASE ---------------")
	stopPrintingFunc, err := printNodeInfoUntilStopped(
		ctx,
		nodeClientsByServiceIds,
	)
	require.NoError(t, err, "An error occurred launching the node info printer thread")
	defer stopPrintingFunc()

	log.Printf("------------ CHECKING ALL NODES ARE IN SYNC AT BLOCK '%d' ---------------", minBlocksBeforeDeployment)
	syncedBlockNumber, err := waitUntilAllNodesGetSynced(ctx, idsToQuery, nodeClientsByServiceIds, minBlocksBeforeDeployment)
	require.NoError(t, err, "An error occurred waiting until all nodes get synced before inducing the partition")
	log.Printf("------------ ALL NODES SYNCED AT BLOCK NUMBER '%v' ------------", syncedBlockNumber)
	printAllNodesInfo(ctx, nodeClientsByServiceIds)
	log.Printf("------------ VERIFIED ALL NODES ARE IN SYNC BEFORE SENDING THE TX ------------")

	log.Printf("------------ SENDING A TX THAT RESOLVES THE DEPOSIT CONTRACT LEAF ------------")
	client := nodeClientsByServiceIds["el-client-0"]
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		t.Fatal("could not get nonce")
	}
	// Send 1 wei to an account designed to resolve the leaf node in which the account
	// data was stored.
	depositleafnoderesolvingaddr := common.HexToAddress("2e1a912c2ba698c9e4c0193f93c598f5800ffc85")
	tx, _ := types.SignTx(types.NewTransaction(nonce, depositleafnoderesolvingaddr, big.NewInt(1), 600000, big.NewInt(875000000), nil), signer, testkey)
	err = client.SendTransaction(ctx, tx)
	if err != nil {
		t.Fatalf("error sending the transaction: %v", err)
	}

	log.Printf("------------ CHECKING ALL NODES ARE STILL IN SYNC AT BLOCK '%d' ---------------", minBlocksBeforeDeployment+minBlocksAfterDeployment)
	syncedBlockNumber, err = waitUntilAllNodesGetSynced(ctx, idsToQuery, nodeClientsByServiceIds, minBlocksBeforeDeployment+minBlocksAfterDeployment)
	require.NoError(t, err, "An error occurred waiting until all nodes get synced after inducing the partition")
	log.Printf("----------- ALL NODES SYNCED AT BLOCK NUMBER '%v' -----------", syncedBlockNumber)
	printAllNodesInfo(ctx, nodeClientsByServiceIds)
	log.Printf("----------- VERIFIED THAT ALL NODES ARE IN SYNC AFTER DEPLOYING TX --------------")

	// from := common.HexToAddress("0xAb2A01BC351770D09611Ac80f1DE076D56E0487d")
	log.Printf("----------- VERIFIED THAT THE TX HAS BEEN INCLUDED -------------")

	// Test teardown phase
	isTestInExecution = false
	log.Printf("------------ TEST FINISHED ---------------")
}

func compareContractData(expectedContractData []byte, receivedContractData []byte) error {
	if !bytes.Equal(expectedContractData, receivedContractData) {
		return stacktrace.NewError("Contract deployment unsuccessful!")
	}
	return nil
}
func initNodeIdsAndRenderModuleParam() string {
	participantParams := make([]string, numParticipants)
	for idx := 0; idx < numParticipants; idx++ {
		nodeIds[idx] = idx
		idsToQuery[idx] = renderServiceId(elNodeIdTemplate, nodeIds[idx])
		participantParams[idx] = participantParam
	}
	return strings.ReplaceAll(moduleParamsTemplate, participantsPlaceholder, strings.Join(participantParams, ","))
}

func getElNodeClientsByServiceID(
	enclaveCtx *enclaves.EnclaveContext,
	serviceIds []services.ServiceID,
) (
	resultNodeClientsByServiceId map[services.ServiceID]*ethclient.Client,
	resultErr error,
) {
	nodeClientsByServiceIds := map[services.ServiceID]*ethclient.Client{}
	for _, serviceId := range serviceIds {
		serviceCtx, err := enclaveCtx.GetServiceContext(serviceId)
		if err != nil {
			return nil, stacktrace.Propagate(err, "A fatal error occurred getting context for service '%v'", serviceId)
		}

		rpcPort, found := serviceCtx.GetPublicPorts()[rpcPortId]
		if !found {
			return nil, stacktrace.NewError("Service '%v' doesn't have expected RPC port with ID '%v'", serviceId, rpcPortId)
		}

		url := fmt.Sprintf(
			"http://%v:%v",
			serviceCtx.GetMaybePublicIPAddress(),
			rpcPort.GetNumber(),
		)
		client, err := ethclient.Dial(url)
		if err != nil {
			return nil, stacktrace.Propagate(err, "A fatal error occurred creating the ETH client for service '%v'", serviceId)
		}

		nodeClientsByServiceIds[serviceId] = client
	}
	return nodeClientsByServiceIds, nil
}

func printNodeInfoUntilStopped(
	ctx context.Context,
	nodeClientsByServiceIds map[services.ServiceID]*ethclient.Client,
) (func(), error) {

	printingStopChan := make(chan struct{})

	printHeader(nodeClientsByServiceIds)
	go func() {
		for {
			select {
			case <-time.Tick(6 * time.Second):
				printAllNodesInfo(ctx, nodeClientsByServiceIds)
			case <-printingStopChan:
				break
			}
		}
	}()

	stopFunc := func() {
		printingStopChan <- struct{}{}
	}

	return stopFunc, nil
}

func getMostRecentNodeBlockWithRetries(
	ctx context.Context,
	serviceId services.ServiceID,
	client *ethclient.Client,
	attempts int,
	sleep time.Duration,
) (*types.Block, error) {

	var resultBlock *types.Block
	var resultErr error

	blockNumberUint64, err := client.BlockNumber(ctx)
	if err != nil {
		resultErr = stacktrace.Propagate(err, "%-25sAn error occurred getting the block number", serviceId)
	}

	if resultErr == nil {
		blockNumberBigint := new(big.Int).SetUint64(blockNumberUint64)
		resultBlock, err = client.BlockByNumber(ctx, blockNumberBigint)
		if err != nil {
			resultErr = stacktrace.Propagate(err, "%-25sAn error occurred getting the latest block", serviceId)
		}
		if resultBlock == nil {
			resultErr = stacktrace.NewError("Something unexpected happened, block mustn't be nil; this is an error in the Geth client")
		}
	}

	if resultErr != nil {
		//Sometimes the client do not find the block, so we do retries
		if attempts--; attempts > 0 {
			time.Sleep(sleep)
			return getMostRecentNodeBlockWithRetries(ctx, serviceId, client, attempts, sleep)
		}
	}

	return resultBlock, resultErr
}

func printHeader(nodeClientsByServiceIds map[services.ServiceID]*ethclient.Client) {
	nodeInfoHeaderStr := nodeInfoPrefix
	nodeInfoHeaderLine2Str := nodeInfoPrefix

	sortedServiceIds := make([]services.ServiceID, 0, len(nodeClientsByServiceIds))
	for serviceId := range nodeClientsByServiceIds {
		sortedServiceIds = append(sortedServiceIds, serviceId)
	}
	sort.Slice(sortedServiceIds, func(i, j int) bool {
		return sortedServiceIds[i] < sortedServiceIds[j]
	})
	for _, serviceId := range sortedServiceIds {
		nodeInfoHeaderStr = fmt.Sprintf(nodeInfoHeaderStr+"  %-18s  |", serviceId)
		nodeInfoHeaderLine2Str = fmt.Sprintf(nodeInfoHeaderLine2Str+"  %-05s - %-10s  |", "block", "hash")
	}
	log.Print(nodeInfoHeaderStr)
	log.Print(nodeInfoHeaderLine2Str)
}

func printAllNodesInfo(ctx context.Context, nodeClientsByServiceIds map[services.ServiceID]*ethclient.Client) {
	nodesCurrentBlock := make(map[services.ServiceID]*types.Block, 4)
	for serviceId, client := range nodeClientsByServiceIds {
		nodeBlock, err := getMostRecentNodeBlockWithRetries(ctx, serviceId, client, retriesAttempts, retriesSleepDuration)
		if err != nil && isTestInExecution {
			log.Printf("%-25sAn error occurred getting the most recent block, err:\n%v", serviceId, err.Error())
		}
		nodesCurrentBlock[serviceId] = nodeBlock
	}
	printAllNodesCurrentBlock(nodesCurrentBlock)
}

func printAllNodesCurrentBlock(nodeCurrentBlocks map[services.ServiceID]*types.Block) {
	if nodeCurrentBlocks == nil {
		return
	}
	nodeInfoStr := nodeInfoPrefix
	sortedServiceIds := make([]services.ServiceID, 0, len(nodeCurrentBlocks))
	for serviceId := range nodeCurrentBlocks {
		sortedServiceIds = append(sortedServiceIds, serviceId)
	}
	sort.Slice(sortedServiceIds, func(i, j int) bool {
		return sortedServiceIds[i] < sortedServiceIds[j]
	})

	for _, serviceId := range sortedServiceIds {
		blockInfo := nodeCurrentBlocks[serviceId]
		// hack, it looks like shutting down the system has some
		// non-determinism
		if blockInfo == nil {
			continue
		}
		hash := blockInfo.Hash().Hex()
		shortHash := hash[:5] + ".." + hash[len(hash)-3:]
		nodeInfoStr = fmt.Sprintf(nodeInfoStr+"  %05d - %-10s  |", blockInfo.NumberU64(), shortHash)
	}
	log.Print(nodeInfoStr)
}

func getMostRecentBlockAndStoreIt(
	ctx context.Context,
	serviceId services.ServiceID,
	serviceClient *ethclient.Client,
	nodeBlocksByServiceIds *sync.Map,
) error {
	block, err := getMostRecentNodeBlockWithRetries(ctx, serviceId, serviceClient, retriesAttempts, retriesSleepDuration)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred getting the most recent node block for service '%v'", serviceId)
	}

	nodeBlocksByServiceIds.Store(serviceId, block)

	return nil
}

func waitUntilAllNodesGetSynced(
	ctx context.Context,
	serviceIds []services.ServiceID,
	nodeClientsByServiceIds map[services.ServiceID]*ethclient.Client,
	minimumBlockNumberConstraint uint64,
) (uint64, error) {
	var wg sync.WaitGroup
	nodeBlocksByServiceIds := &sync.Map{}
	errorChan := make(chan error, 1)
	defer close(errorChan)

	for {
		select {
		case <-time.Tick(1 * time.Second):
			for _, serviceId := range serviceIds {
				wg.Add(1)
				nodeServiceId := serviceId
				nodeClient := nodeClientsByServiceIds[serviceId]
				go func() {
					defer wg.Done()

					if err := getMostRecentBlockAndStoreIt(ctx, nodeServiceId, nodeClient, nodeBlocksByServiceIds); err != nil {
						errorChan <- stacktrace.Propagate(err, "An error occurred getting the most recent node block and storing it for service '%v'", nodeServiceId)
					}
				}()
			}
			wg.Wait()

			var previousNodeBlockHash string
			var syncedBlockNumber uint64

			areAllEqual := true

			for _, serviceId := range serviceIds {

				uncastedNodeBlock, ok := nodeBlocksByServiceIds.LoadAndDelete(serviceId)
				if !ok {
					errorChan <- stacktrace.NewError("An error occurred loading the node's block for service with ID '%v'", serviceId)
					break
				}
				nodeBlock := uncastedNodeBlock.(*types.Block)
				nodeBlockHash := nodeBlock.Hash().Hex()

				if previousNodeBlockHash != "" && previousNodeBlockHash != nodeBlockHash {
					areAllEqual = false
					break
				}

				previousNodeBlockHash = nodeBlockHash
				syncedBlockNumber = nodeBlock.NumberU64()
			}

			if areAllEqual && syncedBlockNumber >= minimumBlockNumberConstraint {
				return syncedBlockNumber, nil
			}

		case err := <-errorChan:
			if err != nil {
				return 0, stacktrace.Propagate(err, "An error occurred checking for synced nodes")
			}
			return 0, stacktrace.NewError("Something unexpected happened, a new value was received from the error channel but it's nil")
		}
	}
}

func renderServiceId(template string, nodeId int) services.ServiceID {
	return services.ServiceID(fmt.Sprintf(template, nodeId))
}
