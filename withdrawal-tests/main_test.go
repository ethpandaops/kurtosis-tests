package main

import (
	"context"
	"fmt"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethpandaops/beacon/pkg/beacon"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/core/lib/enclaves"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/core/lib/services"
	"github.com/kurtosis-tech/kurtosis-sdk/api/golang/engine/lib/kurtosis_context"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"log"
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
	testName              = "go-ethereum-testnet-with-contract"
	isPartitioningEnabled = true

	nodeInfoPrefix = "NODES STATUS -- |"

	eth2StarlarkPackage = "github.com/kurtosis-tech/eth2-package"

	// must be something greater than 4 to have at least 2 nodes in each partition
	numParticipants = 4

	participantsPlaceholder = "{{participants_param}}"
	//TODO: Replace with image pulled from commit ref
	//participantParam        = `{"elType":"geth","elImage":"ethereum/client-go:v1.10.25","clType":"lodestar","clImage":"chainsafe/lodestar:v1.1.0"}`
	participantParam = `{"el_client_type":"geth","el_client_image":"ethereum/client-go:v1.10.26","cl_client_type":"lighthouse","cl_client_image":"sigp/lighthouse:v3.3.0"}`
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

	minSlotsBeforeDeployment = 5
	minSlotsAfterDeployment  = 15

	elNodeIdTemplate          = "el-client-%d"
	clNodeBeaconIdTemplate    = "cl-client-%d-beacon"
	clNodeValidatorIdTemplate = "cl-client-%d-validator"

	rpcPortId            = "rpc"
	beaconHttpPortId     = "http"
	retriesAttempts      = 20
	retriesSleepDuration = 10 * time.Millisecond
)

var (
	nodeIds           = make([]int, numParticipants)
	elIdsToQuery      = make([]services.ServiceID, numParticipants)
	clIdsToQuery      = make([]services.ServiceID, numParticipants)
	isTestInExecution bool
)

func TestBasicTestnetFinality(t *testing.T) {
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

	//nodeELClientsByServiceIds, err := getElNodeClientsByServiceID(enclaveCtx, elIdsToQuery)
	require.NoError(t, err, "An error occurred when trying to get the node clients for services with IDs '%+v'", clIdsToQuery)

	nodeCLClientsByServiceIds, err := getCLNodeClientsByServiceID(enclaveCtx, clIdsToQuery)
	SetCLClientState("start", ctx, nodeCLClientsByServiceIds)
	//printCLNodeInfo(ctx, nodeCLClientsByServiceIds)

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
	SetCLClientState("stop", ctx, nodeCLClientsByServiceIds)
	// Test teardown phase
	isTestInExecution = false
	log.Printf("------------ TEST FINISHED ---------------")
}

func initNodeIdsAndRenderModuleParam() string {
	participantParams := make([]string, numParticipants)
	for idx := 0; idx < numParticipants; idx++ {
		nodeIds[idx] = idx
		elIdsToQuery[idx] = renderServiceId(elNodeIdTemplate, nodeIds[idx])
		clIdsToQuery[idx] = renderServiceId(clNodeBeaconIdTemplate, nodeIds[idx])
		participantParams[idx] = participantParam

	}
	return strings.ReplaceAll(moduleParamsTemplate, participantsPlaceholder, strings.Join(participantParams, ","))
}

func SetCLClientState(Switch string, ctx context.Context, nodeClientsByServiceIds map[services.ServiceID]beacon.Node) {
	if Switch == "start" {
		for _, client := range nodeClientsByServiceIds {
			// Start the beacon node. Start will wait until the beacon node is ready.
			if err := client.Start(ctx); err != nil {
				log.Fatal(err)
			}
		}
	} else if Switch == "stop" {
		for _, client := range nodeClientsByServiceIds {
			// Stop the beacon node.
			if err := client.Stop(ctx); err != nil {
				log.Fatal(err)
			}
		}
	} else {
		log.Fatal("Invalid switch")
	}
}

//func printCLNodeInfo(ctx context.Context, nodeClientsByServiceIds map[services.ServiceID]beacon.Node) {
//
//	for _, client := range nodeClientsByServiceIds {
//		block, err := client.FetchBlock(ctx, "head")
//		if err != nil {
//			log.Fatal(err)
//		}
//
//	}
//
//}

func getCLNodeClientsByServiceID(
	enclaveCtx *enclaves.EnclaveContext,
	serviceIds []services.ServiceID,
) (
	resultNodeClientsByServiceId map[services.ServiceID]beacon.Node,
	resultErr error,
) {
	nodeClientsByServiceIds := map[services.ServiceID]beacon.Node{}
	config := beacon.Config{}

	for _, serviceId := range serviceIds {
		serviceCtx, err := enclaveCtx.GetServiceContext(serviceId)
		if err != nil {
			return nil, stacktrace.Propagate(err, "A fatal error occurred getting context for service '%v'", serviceId)
		}
		rpcPort, found := serviceCtx.GetPublicPorts()[beaconHttpPortId]

		if !found {
			return nil, stacktrace.NewError("Service '%v' doesn't have expected RPC port with ID '%v'", serviceId, rpcPortId)
		}

		config.Addr = fmt.Sprintf("http://%v:%v", serviceCtx.GetMaybePublicIPAddress(), rpcPort.GetNumber())
		var logger = logrus.New()
		opts := *beacon.DefaultOptions().DisablePrometheusMetrics().DisableEmptySlotDetection()

		client := beacon.NewNode(logger, &config, "eth", opts)

		if err != nil {
			return nil, stacktrace.Propagate(err, "A fatal error occurred creating the ETH client for service '%v'", serviceId)
		}

		nodeClientsByServiceIds[serviceId] = client

	}
	return nodeClientsByServiceIds, nil
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
	nodeClientsByServiceIds map[services.ServiceID]beacon.Node,
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

func getMostRecentNodeBlockWithRetries(
	ctx context.Context,
	serviceId services.ServiceID,
	client beacon.Node,
) (*spec.VersionedSignedBeaconBlock, error) {

	var resultErr error

	block, err := client.FetchBlock(ctx, "head")
	if err != nil {
		resultErr = stacktrace.Propagate(err, "%-25sAn error occurred getting the latest block", serviceId)
	}

	return block, resultErr
}

func printHeader(nodeClientsByServiceIds map[services.ServiceID]beacon.Node) {
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

func printAllNodesInfo(ctx context.Context, nodeClientsByServiceIds map[services.ServiceID]beacon.Node) {
	nodesCurrentBlock := make(map[services.ServiceID]*spec.VersionedSignedBeaconBlock, 4)
	for serviceId, client := range nodeClientsByServiceIds {
		nodeBlock, err := getMostRecentNodeBlockWithRetries(ctx, serviceId, client)
		if err != nil && isTestInExecution {
			log.Printf("%-25sAn error occurred getting the most recent block, err:\n%v", serviceId, err.Error())
		}
		nodesCurrentBlock[serviceId] = nodeBlock
	}
	printAllNodesCurrentBlock(nodesCurrentBlock)
}

func printAllNodesCurrentBlock(nodeCurrentBlocks map[services.ServiceID]*spec.VersionedSignedBeaconBlock) {
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
		hash, err := blockInfo.Root()

		if err != nil {
			log.Fatal(err)
		}
		slot, err := blockInfo.Slot()
		shortHash := hash.String()[:5] + ".." + hash.String()[len(hash.String())-3:]
		nodeInfoStr = fmt.Sprintf(nodeInfoStr+"  %05d - %-10s  |", slot, shortHash)
	}
	log.Print(nodeInfoStr)
}

func getMostRecentBlockAndStoreIt(
	ctx context.Context,
	serviceId services.ServiceID,
	serviceClient beacon.Node,
	nodeBlocksByServiceIds *sync.Map,
) error {
	block, err := getMostRecentNodeBlockWithRetries(ctx, serviceId, serviceClient)
	if err != nil {
		return stacktrace.Propagate(err, "An error occurred getting the most recent node block for service '%v'", serviceId)
	}

	nodeBlocksByServiceIds.Store(serviceId, block)

	return nil
}

// TODO Broken function
func waitUntilAllNodesGetSynced(
	ctx context.Context,
	serviceIds []services.ServiceID,
	nodeClientsByServiceIds map[services.ServiceID]beacon.Node,
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

			var previousNodeBlockHash phase0.Root
			var syncedBlockNumber phase0.Slot

			areAllEqual := true

			for _, serviceId := range serviceIds {

				uncastedNodeBlock, ok := nodeBlocksByServiceIds.LoadAndDelete(serviceId)
				if !ok {
					errorChan <- stacktrace.NewError("An error occurred loading the node's block for service with ID '%v'", serviceId)
					break
				}
				nodeBlock := uncastedNodeBlock.(*spec.VersionedSignedBeaconBlock)

				nodeBlockHash, err := nodeBlock.BodyRoot()
				if err != nil {
					stacktrace.Propagate(err, "An error occurred getting the node block hash for service with ID '%v'", serviceId)
				}
				nodeStateRoot, err := nodeBlock.StateRoot()
				if err != nil {
					stacktrace.Propagate(err, "An error occurred getting the node state root for service with ID '%v'", serviceId)
				}

				nodeRoot, err := nodeBlock.Root()
				if err != nil {
					stacktrace.Propagate(err, "An error occurred getting the node hash tree root for service with ID '%v'", serviceId)
				}
				fmt.Println(uncastedNodeBlock)
				fmt.Println("Block Root: ", nodeBlockHash.String())
				fmt.Println("State Root: ", nodeStateRoot.String())
				fmt.Println("Node root", nodeRoot.String())

				if previousNodeBlockHash.String() != "0x0000000000000000000000000000000000000000000000000000000000000000" && previousNodeBlockHash.String() != nodeBlockHash.String() {
					areAllEqual = false
					break
				}

				previousNodeBlockHash = nodeBlockHash
				syncedBlockNumber, err = nodeBlock.Slot()
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

func renderServiceId(template string, nodeId int) services.ServiceID {
	return services.ServiceID(fmt.Sprintf(template, nodeId))
}
