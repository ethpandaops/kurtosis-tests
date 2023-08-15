package main

import (
	"context"
	"fmt"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethpandaops/beacon/pkg/beacon"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/enclaves"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/services"
	"github.com/kurtosis-tech/kurtosis/api/golang/engine/lib/kurtosis_context"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/protolambda/eth2api"
	"github.com/protolambda/eth2api/client/beaconapi"
	"github.com/protolambda/zrnt/eth2/beacon/common"
	"github.com/protolambda/zrnt/eth2/configs"
	"github.com/stretchr/testify/require"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

/*
This example will:
1. Start an Ethereum network with `numParticipants` nodes
2. Wait for all the nodes to be synced
3. Wait for the block production to continue
4. Assert that there was no forking

This test demonstrates basic Ethereum testnet behaviour in Kurtosis.
*/

const (
	testName = "go-ethereum-testnet-with-contract"

	nodeInfoPrefix = "NODES STATUS -- |"

	eth2StarlarkPackage = "github.com/kurtosis-tech/eth2-package"

	// must be something greater than 4 to have at least 2 nodes in each partition
	numParticipants = 4

	participantsPlaceholder = "{{participants_param}}"
	//TODO: Replace with image pulled from commit ref
	//participantParam        = `{"elType":"geth","elImage":"ethereum/client-go:v1.10.25","clType":"lodestar","clImage":"chainsafe/lodestar:v1.1.0"}`
	participantParam = `{"el_client_type":"geth","el_client_image":"ethereum/client-go:v1.11.2","cl_client_type":"lighthouse","cl_client_image":"sigp/lighthouse:v3.5.0"}`
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
	"network_params":
		{	"network_id": "3151908", 
			"preregistered_validator_keys_mnemonic": "giant issue aisle success illegal bike spike question tent bar rely arctic volcano long crawl hungry vocal artwork sniff fantasy very lucky have athlete"
		}
}`

	minSlotsBeforeDeployment = 5
	minSlotsAfterDeployment  = 5

	elNodeIdTemplate       = "el-%d-geth-lighthouse"
	clNodeBeaconIdTemplate = "cl-%d-lighthouse-geth"

	rpcPortId        = "rpc"
	beaconHttpPortId = "http"

	relativePathToMainFile = ""
	mainFunctionName       = ""
)

var (
	nodeIds           = make([]int, numParticipants)
	elIdsToQuery      = make([]services.ServiceUUID, numParticipants)
	clIdsToQuery      = make([]services.ServiceUUID, numParticipants)
	isTestInExecution bool
)

func TestBasicTestnetFinality(t *testing.T) {
	isTestInExecution = true

	moduleParams := initNodeIdsAndRenderModuleParam()

	ctx := context.Background()

	log.Printf("------------ CONNECTING TO KURTOSIS ENGINE ---------------")
	kurtosisCtx, err := kurtosis_context.NewKurtosisContextFromLocalEngine()
	require.NoError(t, err, "An error occurred connecting to the Kurtosis engine")

	enclaveName := fmt.Sprintf(
		"%v-%v",
		testName, time.Now().Unix(),
	)
	enclaveCtx, err := kurtosisCtx.CreateEnclave(ctx, enclaveName)
	require.NoError(t, err, "An error occurred creating the enclave")
	defer func() {
		if !isTestInExecution {
			_ = kurtosisCtx.DestroyEnclave(ctx, enclaveName)
			_, _ = kurtosisCtx.Clean(ctx, false)
		}
	}()

	log.Printf("------------ EXECUTING MODULE ---------------")
	starlarkRunResult, err := enclaveCtx.RunStarlarkRemotePackageBlocking(ctx, eth2StarlarkPackage, relativePathToMainFile, mainFunctionName, moduleParams, false, 0, []kurtosis_core_rpc_api_bindings.KurtosisFeatureFlag{})
	require.NoError(t, err, "An error executing loading the ETH module")
	require.Nil(t, starlarkRunResult.InterpretationError)
	require.Empty(t, starlarkRunResult.ValidationErrors)
	require.Nil(t, starlarkRunResult.ExecutionError)

	require.NoError(t, err, "An error occurred when trying to get the node clients for services with IDs '%+v'", clIdsToQuery)

	nodeCLClientsByServiceIds, err := getCLNodeClientsByServiceName(enclaveCtx, clIdsToQuery)

	require.NoError(t, err, "An error occurred when trying to get the node clients for services with IDs '%+v'", clIdsToQuery)

	log.Printf("------------ STARTING TEST CASE ---------------")
	stopPrintingFunc, err := printNodeInfoUntilStopped(
		ctx,
		nodeCLClientsByServiceIds,
	)
	require.NoError(t, err, "An error occurred launching the node info printer thread")
	defer stopPrintingFunc()

	log.Printf("------------ CHECKING ALL NODES ARE IN SYNC AT BLOCK '%d' ---------------", minSlotsBeforeDeployment)
	syncedBlockNumber, err := waitUntilAllNodesGetSynced(ctx, clIdsToQuery, nodeCLClientsByServiceIds, minSlotsBeforeDeployment)
	require.NoError(t, err, "An error occurred waiting until all nodes get synced")
	log.Printf("------------ ALL NODES SYNCED AT BLOCK NUMBER '%v' ------------", syncedBlockNumber)
	printAllNodesInfo(ctx, nodeCLClientsByServiceIds)
	//printCLNodeInfo(ctx, nodeCLClientsByServiceIds)
	log.Printf("------------ VERIFIED ALL NODES ARE IN SYNC ------------")

	syncedBlockNumber, err = waitUntilAllNodesGetSynced(ctx, clIdsToQuery, nodeCLClientsByServiceIds, minSlotsBeforeDeployment+minSlotsAfterDeployment)
	require.NoError(t, err, "An error occurred waiting until all nodes get synced after inducing the partition")
	log.Printf("----------- ALL NODES SYNCED AT BLOCK NUMBER '%v' -----------", syncedBlockNumber)
	printAllNodesInfo(ctx, nodeCLClientsByServiceIds)

	// Test teardown phase
	isTestInExecution = false
	log.Printf("------------ TEST FINISHED ---------------")
}

func initNodeIdsAndRenderModuleParam() string {
	participantParams := make([]string, numParticipants)
	for idx := 1; idx <= numParticipants; idx++ {
		nodeIds[idx] = idx
		elIdsToQuery[idx] = renderServiceId(elNodeIdTemplate, nodeIds[idx])
		clIdsToQuery[idx] = renderServiceId(clNodeBeaconIdTemplate, nodeIds[idx])
		participantParams[idx] = participantParam

	}
	return strings.ReplaceAll(moduleParamsTemplate, participantsPlaceholder, strings.Join(participantParams, ","))
}

func getCLNodeClientsByServiceName(
	enclaveCtx *enclaves.EnclaveContext,
	serviceIds []services.ServiceUUID,
) (
	resultNodeClientsByServiceId map[services.ServiceUUID]eth2api.Eth2HttpClient,
	resultErr error,
) {
	nodeClientsByServiceIds := map[services.ServiceUUID]eth2api.Eth2HttpClient{}
	config := beacon.Config{}

	for _, serviceId := range serviceIds {
		serviceCtx, err := enclaveCtx.GetServiceContext(string(serviceId))
		if err != nil {
			return nil, stacktrace.Propagate(err, "A fatal error occurred getting context for service '%v'", serviceId)
		}
		rpcPort, found := serviceCtx.GetPublicPorts()[beaconHttpPortId]

		if !found {
			return nil, stacktrace.NewError("Service '%v' doesn't have expected RPC port with ID '%v'", serviceId, rpcPortId)
		}

		config.Addr = fmt.Sprintf("http://%v:%v", serviceCtx.GetMaybePublicIPAddress(), rpcPort.GetNumber())

		client := &eth2api.Eth2HttpClient{
			Addr: config.Addr,
			Cli: &http.Client{
				Transport: &http.Transport{
					MaxIdleConnsPerHost: 123,
				},
				Timeout: 10 * time.Second,
			},
			Codec: eth2api.JSONCodec{},
		}

		if err != nil {
			return nil, stacktrace.Propagate(err, "A fatal error occurred creating the ETH client for service '%v'", serviceId)
		}

		nodeClientsByServiceIds[serviceId] = *client

	}
	return nodeClientsByServiceIds, nil
}

func getElNodeClientsByServiceName(
	enclaveCtx *enclaves.EnclaveContext,
	serviceIds []services.ServiceUUID,
) (
	resultNodeClientsByServiceId map[services.ServiceUUID]*ethclient.Client,
	resultErr error,
) {
	nodeClientsByServiceIds := map[services.ServiceUUID]*ethclient.Client{}
	for _, serviceId := range serviceIds {
		serviceCtx, err := enclaveCtx.GetServiceContext(string(serviceId))
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
	nodeClientsByServiceIds map[services.ServiceUUID]eth2api.Eth2HttpClient,
) (func(), error) {

	printingStopChan := make(chan struct{})

	printHeader(nodeClientsByServiceIds)
	go func() {
		for {
			select {
			case <-time.Tick(6 * time.Second):
				printAllNodesInfo(ctx, nodeClientsByServiceIds)
			case <-printingStopChan:
				return
			}
		}
	}()

	stopFunc := func() {
		fmt.Println("=== stopping ===")
		printingStopChan <- struct{}{}
	}

	return stopFunc, nil
}

func getMostRecentNodeBlockWithRetries(ctx context.Context, client eth2api.Eth2HttpClient) (*common.BeaconBlockEnvelope, error) {

	var resultErr error
	var block eth2api.VersionedSignedBeaconBlock
	var blockEnvelope *common.BeaconBlockEnvelope

	var genesis eth2api.GenesisResponse
	if exists, err := beaconapi.Genesis(ctx, &client, &genesis); !exists {
		fmt.Println("chain did not start yet")
		os.Exit(1)
	} else if err != nil {
		fmt.Println("failed to get genesis", err)
		os.Exit(1)
	}
	spec := configs.Mainnet
	bellatrixForkDigest := common.ComputeForkDigest(spec.ALTAIR_FORK_VERSION, genesis.GenesisValidatorsRoot)

	if exists, err := beaconapi.BlockV2(ctx, &client, eth2api.BlockHead, &block); !exists {
		fmt.Println("block not found")
		os.Exit(1)
	} else if err != nil {
		fmt.Println("failed to get block", err)
		os.Exit(1)
	} else {

		// add digest:
		blockEnvelope = block.Data.Envelope(spec, bellatrixForkDigest)
	}

	return blockEnvelope, resultErr
}

func printHeader(nodeClientsByServiceIds map[services.ServiceUUID]eth2api.Eth2HttpClient) {
	nodeInfoHeaderStr := nodeInfoPrefix
	nodeInfoHeaderLine2Str := nodeInfoPrefix

	sortedServiceIds := make([]services.ServiceUUID, 0, len(nodeClientsByServiceIds))
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

func printAllNodesInfo(ctx context.Context, nodeClientsByServiceIds map[services.ServiceUUID]eth2api.Eth2HttpClient) {
	nodesCurrentBlock := make(map[services.ServiceUUID]*common.BeaconBlockEnvelope, 4)
	for serviceId, client := range nodeClientsByServiceIds {
		nodeBlock, err := getMostRecentNodeBlockWithRetries(ctx, client)
		if err != nil && isTestInExecution {
			log.Printf("%-25sAn error occurred getting the most recent block, err:\n%v", serviceId, err.Error())
		}
		nodesCurrentBlock[serviceId] = nodeBlock
	}
	printAllNodesCurrentBlock(nodesCurrentBlock)
}

func printAllNodesCurrentBlock(nodeCurrentBlocks map[services.ServiceUUID]*common.BeaconBlockEnvelope) {
	if nodeCurrentBlocks == nil {
		return
	}
	nodeInfoStr := nodeInfoPrefix
	sortedServiceIds := make([]services.ServiceUUID, 0, len(nodeCurrentBlocks))
	for serviceId := range nodeCurrentBlocks {
		sortedServiceIds = append(sortedServiceIds, serviceId)
	}
	sort.Slice(sortedServiceIds, func(i, j int) bool {
		return sortedServiceIds[i] < sortedServiceIds[j]
	})

	for _, serviceId := range sortedServiceIds {
		blockInfo := nodeCurrentBlocks[serviceId]
		hash := blockInfo.BlockRoot
		//fmt.Println(blockInfo.Slot)
		slot := blockInfo.Slot
		shortHash := hash.String()[:5] + ".." + hash.String()[len(hash.String())-3:]
		nodeInfoStr = fmt.Sprintf(nodeInfoStr+"  %05d - %-10s  |", slot, shortHash)
	}
	log.Print(nodeInfoStr)
}

func getMostRecentBlockAndStoreIt(
	ctx context.Context,
	serviceId services.ServiceUUID,
	serviceClient eth2api.Eth2HttpClient,
	nodeBlocksByServiceIds *sync.Map,
) error {
	block, err := getMostRecentNodeBlockWithRetries(ctx, serviceClient)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred getting the most recent node block for service '%v'", serviceId)
	}

	nodeBlocksByServiceIds.Store(serviceId, block)

	return nil
}

func waitUntilAllNodesGetSynced(
	ctx context.Context,
	serviceIds []services.ServiceUUID,
	nodeClientsByServiceIds map[services.ServiceUUID]eth2api.Eth2HttpClient,
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

			var previousNodeBlockHash common.Root
			var syncedBlockNumber common.Slot

			areAllEqual := true

			for _, serviceId := range serviceIds {

				uncastedNodeBlock, ok := nodeBlocksByServiceIds.LoadAndDelete(serviceId)
				if !ok {
					errorChan <- stacktrace.NewError("An error occurred loading the node's block for service with ID '%v'", serviceId)
					break
				}

				nodeBlock := uncastedNodeBlock.(*common.BeaconBlockEnvelope)

				nodeBlockRoot := nodeBlock.BlockRoot

				if previousNodeBlockHash.String() != "0x0000000000000000000000000000000000000000000000000000000000000000" && previousNodeBlockHash.String() != nodeBlockRoot.String() {
					areAllEqual = false
					break
				}

				previousNodeBlockHash = nodeBlockRoot
				syncedBlockNumber = nodeBlock.Slot
			}

			if areAllEqual && uint64(syncedBlockNumber) >= minimumBlockNumberConstraint {
				return uint64(syncedBlockNumber), nil
			}

		case err := <-errorChan:
			if err != nil {
				return 0, stacktrace.Propagate(err, "An error occurred checking for synced nodes")
			}
			return 0, stacktrace.NewError("Something unexpected happened, a new value was received from the error channel but it's nil")
		}
	}
}

func renderServiceId(template string, nodeId int) services.ServiceUUID {
	return services.ServiceUUID(fmt.Sprintf(template, nodeId))
}
