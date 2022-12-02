package main

import (
	"context"
	"fmt"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/core/kurtosis_core_rpc_api_bindings"
	"math/big"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/params"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/core/lib/enclaves"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/core/lib/services"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/engine/lib/kurtosis_context"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
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
	logLevel = logrus.InfoLevel

	testName              = "go-ethereum-testnet-with-contract"
	isPartitioningEnabled = true

	nodeInfoPrefix = "NODES STATUS -- |"

	eth2StarlarkPackage = "github.com/kurtosis-tech/eth2-package"

	// must be something greater than 4 to have at least 2 nodes in each partition
	numParticipants = 4

	participantsPlaceholder = "{{participants_param}}"
	//participantParam        = `{"elType":"geth","elImage":"ethereum/client-go:v1.10.25","clType":"lodestar","clImage":"chainsafe/lodestar:v1.1.0"}`
	participantParam     = `{"el_client_type":"geth","el_client_image":"ethereum/client-go:v1.10.26","cl_client_type":"lighthouse","cl_client_image":"sigp/lighthouse:v3.3.0"}`
	moduleParamsTemplate = `{
	"launch_additional_services": false,
	"participants": [
		` + participantsPlaceholder + `
	]
}`

	minBlocksBeforeDeployment = 5
	minBlocksAfterDeployment  = 5

	elNodeIdTemplate          = "el-client-%d"
	clNodeBeaconIdTemplate    = "cl-client-%d-beacon"
	clNodeValidatorIdTemplate = "cl-client-%d-validator"

	rpcPortId = "rpc"

	retriesAttempts      = 20
	retriesSleepDuration = 10 * time.Millisecond

	newlineChar = "\n"
)

var (
	nodeIds    = make([]int, numParticipants)
	idsToQuery = make([]services.ServiceID, numParticipants)

	isTestInExecution bool
)

func TestContractDeployment(t *testing.T) {
	logrus.SetLevel(logLevel)
	isTestInExecution = true
	moduleParams := initNodeIdsAndRenderModuleParam()

	ctx := context.Background()

	logrus.Info("------------ CONNECTING TO KURTOSIS ENGINE ---------------")
	kurtosisCtx, err := kurtosis_context.NewKurtosisContextFromLocalEngine()
	require.NoError(t, err, "An error occurred connecting to the Kurtosis engine")

	enclaveId := enclaves.EnclaveID(fmt.Sprintf(
		"%v-%v",
		testName, time.Now().Unix(),
	))
	enclaveCtx, err := kurtosisCtx.CreateEnclave(ctx, enclaveId, isPartitioningEnabled)
	require.NoError(t, err, "An error occurred creating the enclave")
	defer kurtosisCtx.StopEnclave(ctx, enclaveId)

	logrus.Info("------------ EXECUTING MODULE ---------------")
	starlarkResponseLine, _, err := enclaveCtx.RunStarlarkRemotePackage(ctx, eth2StarlarkPackage, moduleParams, false)
	require.NoError(t, err, "An error executing loading the ETH module")
	_, _, interpretationErrors, validationErrors, executionErrors := readStreamContentUntilClosed(starlarkResponseLine)
	require.Nil(t, interpretationErrors)
	require.Empty(t, validationErrors)
	require.Nil(t, executionErrors)

	nodeClientsByServiceIds, err := getElNodeClientsByServiceID(enclaveCtx, idsToQuery)
	require.NoError(t, err, "An error occurred when trying to get the node clients for services with IDs '%+v'", idsToQuery)

	logrus.Info("------------ STARTING TEST CASE ---------------")
	stopPrintingFunc, err := printNodeInfoUntilStopped(
		ctx,
		nodeClientsByServiceIds,
	)
	require.NoError(t, err, "An error occurred launching the node info printer thread")
	defer stopPrintingFunc()

	logrus.Infof("------------ CHECKING ALL NODES ARE IN SYNC AT BLOCK '%d' ---------------", minBlocksBeforeDeployment)
	syncedBlockNumber, err := waitUntilAllNodesGetSynced(ctx, idsToQuery, nodeClientsByServiceIds, minBlocksBeforeDeployment)
	require.NoError(t, err, "An error occurred waiting until all nodes get synced before inducing the partition")
	logrus.Infof("------------ ALL NODES SYNCED AT BLOCK NUMBER '%v' ------------", syncedBlockNumber)
	printAllNodesInfo(ctx, nodeClientsByServiceIds)
	logrus.Info("------------ VERIFIED ALL NODES ARE IN SYNC BEFORE SENDING THE TX ------------")

	logrus.Info("------------ SENDING THE CONTRACT DEPLOYMENT TX ------------")
	contractCode := common.Hex2Bytes("0x60806040526040516100109061017b565b604051809103906000f08015801561002c573d6000803e3d6000fd5b506000806101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555034801561007857600080fd5b5060008067ffffffffffffffff8111156100955761009461024a565b5b6040519080825280601f01601f1916602001820160405280156100c75781602001600182028036833780820191505090505b50905060008060009054906101000a900473ffffffffffffffffffffffffffffffffffffffff1690506020600083833c81610101906101e3565b60405161010d90610187565b61011791906101a3565b604051809103906000f080158015610133573d6000803e3d6000fd5b50600160006101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff160217905550505061029b565b60d58061046783390190565b6102068061053c83390190565b61019d816101d9565b82525050565b60006020820190506101b86000830184610194565b92915050565b6000819050602082019050919050565b600081519050919050565b6000819050919050565b60006101ee826101ce565b826101f8846101be565b905061020381610279565b925060208210156102435761023e7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff8360200360080261028e565b831692505b5050919050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052604160045260246000fd5b600061028582516101d9565b80915050919050565b600082821b905092915050565b6101bd806102aa6000396000f3fe608060405234801561001057600080fd5b506004361061002b5760003560e01c8063f566852414610030575b600080fd5b61003861004e565b6040516100459190610146565b60405180910390f35b6000600160009054906101000a900473ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff166381ca91d36040518163ffffffff1660e01b815260040160206040518083038186803b1580156100b857600080fd5b505afa1580156100cc573d6000803e3d6000fd5b505050506040513d601f19601f820116820180604052508101906100f0919061010a565b905090565b60008151905061010481610170565b92915050565b6000602082840312156101205761011f61016b565b5b600061012e848285016100f5565b91505092915050565b61014081610161565b82525050565b600060208201905061015b6000830184610137565b92915050565b6000819050919050565b600080fd5b61017981610161565b811461018457600080fd5b5056fea2646970667358221220a6a0e11af79f176f9c421b7b12f441356b25f6489b83d38cc828a701720b41f164736f6c63430008070033608060405234801561001057600080fd5b5060b68061001f6000396000f3fe6080604052348015600f57600080fd5b506004361060285760003560e01c8063ab5ed15014602d575b600080fd5b60336047565b604051603e9190605d565b60405180910390f35b60006001905090565b6057816076565b82525050565b6000602082019050607060008301846050565b92915050565b600081905091905056fea26469706673582212203a14eb0d5cd07c277d3e24912f110ddda3e553245a99afc4eeefb2fbae5327aa64736f6c63430008070033608060405234801561001057600080fd5b5060405161020638038061020683398181016040528101906100329190610063565b60018160001c6100429190610090565b60008190555050610145565b60008151905061005d8161012e565b92915050565b60006020828403121561007957610078610129565b5b60006100878482850161004e565b91505092915050565b600061009b826100f0565b91506100a6836100f0565b9250827fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff038211156100db576100da6100fa565b5b828201905092915050565b6000819050919050565b6000819050919050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052601160045260246000fd5b600080fd5b610137816100e6565b811461014257600080fd5b50565b60b3806101536000396000f3fe6080604052348015600f57600080fd5b506004361060285760003560e01c806381ca91d314602d575b600080fd5b60336047565b604051603e9190605a565b60405180910390f35b60005481565b6054816073565b82525050565b6000602082019050606d6000830184604d565b92915050565b600081905091905056fea26469706673582212209bff7098a2f526de1ad499866f27d6d0d6f17b74a413036d6063ca6a0998ca4264736f6c63430008070033")
	config := &params.ChainConfig{
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
	signer := types.LatestSigner(config)
	testkey, _ := crypto.HexToECDSA("ef5177cd0b6b21c87db5a0bf35d4084a8a57a9d6a064f86d51ac85f2b873a4e2")
	tx, _ := types.SignTx(types.NewContractCreation(0, big.NewInt(0), 3000000, big.NewInt(875000000), contractCode), signer, testkey)
	err = nodeClientsByServiceIds["el-client-0"].SendTransaction(ctx, tx)
	if err != nil {
		t.Fatalf("error sending the contract transaction: %v", err)
	}

	logrus.Infof("------------ CHECKING ALL NODES ARE STILL IN SYNC AT BLOCK '%d' ---------------", minBlocksBeforeDeployment+minBlocksAfterDeployment)
	syncedBlockNumber, err = waitUntilAllNodesGetSynced(ctx, idsToQuery, nodeClientsByServiceIds, minBlocksBeforeDeployment+minBlocksAfterDeployment)
	require.NoError(t, err, "An error occurred waiting until all nodes get synced after inducing the partition")
	logrus.Infof("----------- ALL NODES SYNCED AT BLOCK NUMBER '%v' -----------", syncedBlockNumber)
	printAllNodesInfo(ctx, nodeClientsByServiceIds)
	logrus.Info("----------- VERIFIED THAT ALL NODES ARE IN SYNC AFTER HEALING THE PARTITION --------------")

	// TODO verify that the contract deployment is correct
	// nodeClientsByServiceIds[].StorageAt(ctx, addr, common.Hash{}, nil)

	isTestInExecution = false
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
		for true {
			select {
			case <-time.Tick(3 * time.Second):
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
	logrus.Infof(nodeInfoHeaderStr)
	logrus.Infof(nodeInfoHeaderLine2Str)
}

func printAllNodesInfo(ctx context.Context, nodeClientsByServiceIds map[services.ServiceID]*ethclient.Client) {
	nodesCurrentBlock := make(map[services.ServiceID]*types.Block, 4)
	for serviceId, client := range nodeClientsByServiceIds {
		nodeBlock, err := getMostRecentNodeBlockWithRetries(ctx, serviceId, client, retriesAttempts, retriesSleepDuration)
		if err != nil && isTestInExecution {
			logrus.Warnf("%-25sAn error occurred getting the most recent block, err:\n%v", serviceId, err.Error())
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
		hash := blockInfo.Hash().Hex()
		shortHash := hash[:5] + ".." + hash[len(hash)-3:]
		nodeInfoStr = fmt.Sprintf(nodeInfoStr+"  %05d - %-10s  |", blockInfo.NumberU64(), shortHash)
	}
	logrus.Infof(nodeInfoStr)
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

	for true {
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

	return 0, nil
}

func renderServiceId(template string, nodeId int) services.ServiceID {
	return services.ServiceID(fmt.Sprintf(template, nodeId))
}

// TODO remove this when we have a product supported way of doing this
func readStreamContentUntilClosed(responseLines chan *kurtosis_core_rpc_api_bindings.StarlarkRunResponseLine) (string, []*kurtosis_core_rpc_api_bindings.StarlarkInstruction, *kurtosis_core_rpc_api_bindings.StarlarkInterpretationError, []*kurtosis_core_rpc_api_bindings.StarlarkValidationError, *kurtosis_core_rpc_api_bindings.StarlarkExecutionError) {
	scriptOutput := strings.Builder{}
	instructions := make([]*kurtosis_core_rpc_api_bindings.StarlarkInstruction, 0)
	var interpretationError *kurtosis_core_rpc_api_bindings.StarlarkInterpretationError
	validationErrors := make([]*kurtosis_core_rpc_api_bindings.StarlarkValidationError, 0)
	var executionError *kurtosis_core_rpc_api_bindings.StarlarkExecutionError

	for responseLine := range responseLines {
		if responseLine.GetInstruction() != nil {
			instructions = append(instructions, responseLine.GetInstruction())
		} else if responseLine.GetInstructionResult() != nil {
			scriptOutput.WriteString(responseLine.GetInstructionResult().GetSerializedInstructionResult())
			scriptOutput.WriteString(newlineChar)
		} else if responseLine.GetError() != nil {
			if responseLine.GetError().GetInterpretationError() != nil {
				interpretationError = responseLine.GetError().GetInterpretationError()
			} else if responseLine.GetError().GetValidationError() != nil {
				validationErrors = append(validationErrors, responseLine.GetError().GetValidationError())
			} else if responseLine.GetError().GetExecutionError() != nil {
				executionError = responseLine.GetError().GetExecutionError()
			}
		}
	}
	return scriptOutput.String(), instructions, interpretationError, validationErrors, executionError
}
