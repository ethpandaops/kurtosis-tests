package protocolberg

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/starlark_run_config"

	"github.com/ethpandaops/kurtosis-tests/types"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/services"
	"github.com/kurtosis-tech/kurtosis/api/golang/engine/lib/kurtosis_context"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func init() {
	// Set up logger with timestamps and ensure output is visible
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.SetOutput(os.Stdout)
}

const (
	enclaveNamePrefix  = "finalization-test"
	ethPackage         = "github.com/ethpandaops/ethereum-package"
	inputFile          = "./input_args.yaml"
	defaultParallelism = 4
	isNotDryRun        = false

	// we use the main.star file at root of the package
	pathToMainFile = ""
	// the main function is called run
	runFunctionName = ""

	beaconServiceHttpPortId   = "http"
	elServiceRpcPortId        = "rpc"
	websiteApiId              = "api"
	finalizationRetryInterval = time.Second * 10

	secondsPerSlot        = 12
	slotsPerEpoch         = 32
	finalizationEpoch     = 5
	bufferForFinalization = 45
	// 12 seconds per slot, 32 slots per epoch, 5th epoch, some buffer
	timeoutForFinalization = secondsPerSlot*slotsPerEpoch*finalizationEpoch*time.Second + bufferForFinalization*time.Second
	timeoutForSync         = 30 * time.Second
	syncRetryInterval      = 2 * time.Second
	sleepIntervalMessage   = "Pausing querying service '%s' for '%v' seconds"
	fullySyncedMessage     = "Node '%s' is fully synced"
	stillSyncingMessage    = "Node '%s' is still syncing"

	httpLocalhost = "http://0.0.0.0"

	clPrefix        = "cl-"
	elPrefix        = "el-"
	forkmonSuffix   = "-forkmon"
	validatorSuffix = "-validator"

	clSyncingEndpoint    = "eth/v1/node/syncing"
	finalizationEndpoint = "eth/v1/beacon/states/head/finality_checkpoints"
)

var noExperimentalFeatureFlags = []kurtosis_core_rpc_api_bindings.KurtosisFeatureFlag{}

func TestEthPackage_FinalizationSyncing(t *testing.T) {
	// set up the input parameters
	log.Println("Parsing Input Parameters")
	inputParameters, err := os.ReadFile(inputFile)
	require.NoError(t, err, "An error occurred while reading the input file")
	require.NotEmpty(t, inputParameters, "Input parameters byte array is unexpectedly empty")

	var inputParams map[string]interface{}
	err = yaml.Unmarshal(inputParameters, &inputParams)
	require.NoError(t, err, "An error occurred while parsing YAML")

	// Convert back to YAML to ensure proper formatting
	yamlBytes, err := yaml.Marshal(inputParams)
	require.NoError(t, err, "An error occurred while converting to YAML")
	inputParametersAsYAMLString := string(yamlBytes)
	require.NotEmpty(t, inputParametersAsYAMLString, "Input parameter YAML string is unexpectedly empty")

	// set up enclave
	log.Println("Setting up Kurtosis Engine Connection & Enclave")
	ctx, cancelCtxFunc := context.WithCancel(context.Background())
	defer cancelCtxFunc()
	kurtosisCtx, err := kurtosis_context.NewKurtosisContextFromLocalEngine()
	require.NoError(t, err, "An error occurred while creating Kurtosis Context")

	enclaveName := fmt.Sprintf("%s-%d", enclaveNamePrefix, time.Now().Unix())
	enclaveCtx, err := kurtosisCtx.CreateEnclave(ctx, enclaveName)
	require.Nil(t, err, "An unexpected error occurred while creating Enclave Context")
	cleanupEnclavesAsTestsEndedSuccessfully := false
	defer func() {
		if cleanupEnclavesAsTestsEndedSuccessfully {
			kurtosisCtx.DestroyEnclave(ctx, enclaveName)
		}
	}()

	// execute package
	log.Println("Executing the Starlark Package")
	log.Printf("Using Enclave '%v' - use `kurtosis enclave inspect %v` to see whats inside", enclaveName, enclaveName)

	StarlarkConfig := starlark_run_config.StarlarkRunConfig{
		RelativePathToMainFile:   pathToMainFile,
		MainFunctionName:         runFunctionName,
		SerializedParams:         inputParametersAsYAMLString,
		DryRun:                   isNotDryRun,
		Parallelism:              defaultParallelism,
		ExperimentalFeatureFlags: noExperimentalFeatureFlags,
	}

	packageRunResult, err := enclaveCtx.RunStarlarkRemotePackageBlocking(ctx, ethPackage, &StarlarkConfig)
	require.NoError(t, err, "An unexpected error occurred while executing the package")
	require.Nil(t, packageRunResult.InterpretationError)
	require.Empty(t, packageRunResult.ValidationErrors)
	require.Nil(t, packageRunResult.ExecutionError)

	// fetch context for EL and CL RPC
	var beaconNodeServiceContexts []*services.ServiceContext
	var elNodeServiceContexts []*services.ServiceContext
	enclaveServices, err := enclaveCtx.GetServices()
	require.Nil(t, err)
	for serviceName := range enclaveServices {
		serviceNameStr := string(serviceName)
		if strings.HasPrefix(serviceNameStr, clPrefix) && !strings.HasSuffix(serviceNameStr, validatorSuffix) && !strings.HasSuffix(serviceNameStr, forkmonSuffix) {
			log.Printf("Found beacon node with name '%s'", serviceNameStr)
			beaconService, err := enclaveCtx.GetServiceContext(serviceNameStr)
			require.NoError(t, err)
			beaconNodeServiceContexts = append(beaconNodeServiceContexts, beaconService)
		}
		if strings.HasPrefix(serviceNameStr, elPrefix) && !strings.HasSuffix(serviceNameStr, forkmonSuffix) {
			log.Printf("Found el node with name '%s'", serviceNameStr)
			elService, err := enclaveCtx.GetServiceContext(serviceNameStr)
			require.NoError(t, err)
			elNodeServiceContexts = append(elNodeServiceContexts, elService)
		}
	}

	checkFinalizationHasHappened(t, beaconNodeServiceContexts)
	checkAllCLNodesAreSynced(t, beaconNodeServiceContexts)
	checkAllElNodesAreSynced(t, elNodeServiceContexts)

	log.Println("Finalization has happened and all nodes are fully synced")

	cleanupEnclavesAsTestsEndedSuccessfully = true
}

// checkFinalizationHasHappened queries beacon nodes to make sure the finalized epoch is greater than 0
func checkFinalizationHasHappened(t *testing.T, beaconNodeServiceContexts []*services.ServiceContext) {
	// assert that finalization happens on all CL nodes
	wg := sync.WaitGroup{}
	for _, beaconNodeServiceCtx := range beaconNodeServiceContexts {
		wg.Add(1)
		go func(beaconNodeServiceCtx *services.ServiceContext) {
			for {
				publicPorts := beaconNodeServiceCtx.GetPublicPorts()
				beaconHttpPort, found := publicPorts[beaconServiceHttpPortId]
				require.True(t, found)
				epochsFinalized := getFinalizedEpoch(t, beaconHttpPort.GetNumber())
				log.Printf("Queried service '%s' got finalized epoch '%d'", beaconNodeServiceCtx.GetServiceName(), epochsFinalized)
				if epochsFinalized > 0 {
					break
				} else {
					log.Printf(sleepIntervalMessage, beaconNodeServiceCtx.GetServiceName(), finalizationRetryInterval.Seconds())
				}
				time.Sleep(finalizationRetryInterval)
			}
			wg.Done()
		}(beaconNodeServiceCtx)
	}
	didWaitTimeout := didWaitGroupTimeout(&wg, timeoutForFinalization)
	require.False(t, didWaitTimeout, "Finalization didn't happen within expected duration of '%v' seconds", timeoutForFinalization.Seconds())
}

// checkAllCLNodesAreSynced assert that all CL nodes are synced
func checkAllCLNodesAreSynced(t *testing.T, beaconNodeServiceContexts []*services.ServiceContext) {
	clClientSyncWaitGroup := sync.WaitGroup{}
	for _, beaconNodeServiceCtx := range beaconNodeServiceContexts {
		clClientSyncWaitGroup.Add(1)
		go func(beaconNodeServiceCtx *services.ServiceContext) {
			for {
				publicPorts := beaconNodeServiceCtx.GetPublicPorts()
				beaconHttpPort, found := publicPorts[beaconServiceHttpPortId]
				require.True(t, found)
				isSyncing := isCLSyncing(t, beaconHttpPort.GetNumber())
				if !isSyncing {
					log.Printf(fullySyncedMessage, beaconNodeServiceCtx.GetServiceName())
					break
				} else {
					log.Printf(stillSyncingMessage, beaconNodeServiceCtx.GetServiceName())
					log.Printf(sleepIntervalMessage, beaconNodeServiceCtx.GetServiceName(), syncRetryInterval.Seconds())
				}
				time.Sleep(syncRetryInterval)
			}
			clClientSyncWaitGroup.Done()
		}(beaconNodeServiceCtx)
	}
	didWaitTimeout := didWaitGroupTimeout(&clClientSyncWaitGroup, timeoutForSync)
	require.False(t, didWaitTimeout, "CL nodes weren't fully synced in the expected amount of time '%v'", timeoutForSync.Seconds())
}

// checkAllElNodesAreSynced run through every EL node and asserts that its synced or timesout
func checkAllElNodesAreSynced(t *testing.T, elNodeServiceContexts []*services.ServiceContext) {
	elClientSyncWaitGroup := sync.WaitGroup{}
	for _, elNodeServiceCtx := range elNodeServiceContexts {
		elClientSyncWaitGroup.Add(1)
		go func(elNodeServiceCtx *services.ServiceContext) {
			for {
				publicPorts := elNodeServiceCtx.GetPublicPorts()
				rpcPort, found := publicPorts[elServiceRpcPortId]
				require.True(t, found)
				isSyncing := isELSyncing(t, rpcPort.GetNumber())
				if !isSyncing {
					log.Printf(fullySyncedMessage, elNodeServiceCtx.GetServiceName())
					break
				} else {
					log.Printf(stillSyncingMessage, elNodeServiceCtx.GetServiceName())
					log.Printf(sleepIntervalMessage, elNodeServiceCtx.GetServiceName(), syncRetryInterval.Seconds())
				}
				time.Sleep(syncRetryInterval)
			}
			elClientSyncWaitGroup.Done()
		}(elNodeServiceCtx)
	}
	didWaitTimeout := didWaitGroupTimeout(&elClientSyncWaitGroup, timeoutForSync)
	require.False(t, didWaitTimeout, "EL nodes weren't fully synced in the expected amount of time '%v'", timeoutForSync.Seconds())
}

func getFinalizedEpoch(t *testing.T, beaconHttpPort uint16) int {
	url := fmt.Sprintf("%v:%d/%s", httpLocalhost, beaconHttpPort, finalizationEndpoint)
	resp, err := http.Get(url)
	require.Empty(t, err, "an unexpected error happened while making http request")
	require.NotNil(t, resp.Body)
	defer resp.Body.Close()
	var finalizationResponse *types.Finalization
	err = json.NewDecoder(resp.Body).Decode(&finalizationResponse)
	require.Nil(t, err, "an unexpected error occurred while decoding json")
	finalizedEpoch, err := strconv.Atoi(finalizationResponse.Data.Finalized.Epoch)
	require.NoError(t, err, "an error occurred while converting finalized epoch to integer")
	require.GreaterOrEqual(t, finalizedEpoch, 0)
	return finalizedEpoch
}

func isCLSyncing(t *testing.T, beaconHttpPort uint16) bool {
	url := fmt.Sprintf("%v:%d/%s", httpLocalhost, beaconHttpPort, clSyncingEndpoint)
	resp, err := http.Get(url)
	require.Empty(t, err, "an unexpected error happened while making http request")
	require.NotNil(t, resp.Body)
	defer resp.Body.Close()
	var clSyncingResponse *types.ClSyncingStruct
	err = json.NewDecoder(resp.Body).Decode(&clSyncingResponse)
	require.Nil(t, err, "an unexpected error occurred while decoding json")
	isSyncing := clSyncingResponse.Data.IsSyncing
	return isSyncing
}

func isELSyncing(t *testing.T, elRpcPort uint16) bool {
	url := fmt.Sprintf("%v:%d/", httpLocalhost, elRpcPort)
	syncingPost := strings.NewReader(`{"method":"eth_syncing","params":[],"id":1,"jsonrpc":"2.0"}`)
	resp, err := http.Post(url, "application/json", syncingPost)
	require.Empty(t, err, "an unexpected error happened while making http post to EL")
	require.NotNil(t, resp.Body)
	defer resp.Body.Close()
	var elSyncingResponse *types.ElSyncingDataResponse
	err = json.NewDecoder(resp.Body).Decode(&elSyncingResponse)
	require.Nil(t, err, "an unexpected error occurred while decoding json")
	isSyncing := elSyncingResponse.Result
	return isSyncing
}

func didWaitGroupTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	timeoutChannel := make(chan struct{})
	go func() {
		defer close(timeoutChannel)
		wg.Wait()
	}()
	select {
	case <-timeoutChannel:
		return false
	case <-time.After(timeout):
		return true
	}
}
