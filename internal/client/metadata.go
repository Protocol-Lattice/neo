package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func fetchProcedureMetadata(
	ctx context.Context,
	httpClient *http.Client,
	endpoint string,
	headers http.Header,
) ([]ProcedureMeta, error) {
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
