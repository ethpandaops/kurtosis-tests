package types

// Finalization for unmarshalling finalization data
type Finalization struct {
	Data Data `json:"data"`
}

type Data struct {
	Finalized Finalized `json:"finalized"`
}

type Finalized struct {
	Epoch string `json:"epoch"`
}

// ClSyncingStruct for unmarshalling CL syncing data
type ClSyncingStruct struct {
	Data ClSyncingData `json:"data"`
}

type ClSyncingData struct {
	IsSyncing bool `json:"is_syncing"`
}

// ElSyncingDataResponse for unmarshalling EL syncing data
type ElSyncingDataResponse struct {
	Result bool `json:"result"`
}
