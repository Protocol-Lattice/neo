package neo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// MetadataPath is the reserved path under a Neo HTTP prefix that exposes
// procedure metadata as JSON. With the default prefix, the endpoint is
// /neo/_meta.
const MetadataPath = "_meta"

func serveProcedureMetadata(w http.ResponseWriter, r *http.Request, metadata []ProcedureMeta) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeProcedureError(w, NewError(CodeMethodNotAllowed, "metadata requires GET"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	_ = json.NewEncoder(w).Encode(metadata)
}

func fetchProcedureMetadata(ctx context.Context, httpClient *http.Client, endpoint string, headers http.Header) ([]ProcedureMeta, error) {
	ctx = ensureContext(ctx)
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create metadata request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode >= 400 {
		var rpcRes Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			return nil, Errorf(CodeInternal, "metadata failed with status %d", res.StatusCode)
		}
		if rpcRes.Error != "" || rpcRes.Code != "" {
			return nil, responseError(rpcRes)
		}

		return nil, Errorf(CodeInternal, "metadata failed with status %d", res.StatusCode)
	}

	var metadata []ProcedureMeta
	if err := json.NewDecoder(res.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}

	return metadata, nil
}
