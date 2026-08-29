package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	jsoncodec "github.com/Protocol-Lattice/neo/internal/codec/json"
)

// BatchCall describes one unary procedure call in a batch. Every Procedure
// must belong to the client used to invoke Batch.
type BatchCall struct {
	Procedure *ClientProcedure
	Input     any
}

// BatchResult is one result from Batch. Results preserve the request order.
// A non-empty Code or Error represents a procedure error; transport errors are
// returned by Batch itself.
type BatchResult struct {
	Result json.RawMessage `json:"result,omitempty"`
	Code   string          `json:"code,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type batchRequest struct {
	Calls []batchRequestCall `json:"calls"`
}

type batchRequestCall struct {
	Key   string `json:"key"`
	Input any    `json:"input"`
}

type batchResponse struct {
	Results []BatchResult `json:"results"`
}

// Batch sends unary query and mutation calls in one JSON HTTP request. A
// procedure-level failure is recorded in its BatchResult so sibling calls are
// not discarded. Subscriptions are not supported by the batch endpoint.
func (client *Client) Batch(ctx context.Context, calls ...BatchCall) ([]BatchResult, error) {
	if len(calls) == 0 {
		return []BatchResult{}, nil
	}

	requestCalls := make([]batchRequestCall, len(calls))
	for index, call := range calls {
		if call.Procedure == nil {
			return nil, fmt.Errorf("batch call %d: nil procedure", index)
		}
		if call.Procedure.client != client {
			return nil, fmt.Errorf("batch call %d: procedure belongs to another client", index)
		}

		requestCalls[index] = batchRequestCall{
			Key:   call.Procedure.key,
			Input: call.Input,
		}
	}

	body, err := json.Marshal(batchRequest{Calls: requestCalls})
	if err != nil {
		return nil, fmt.Errorf("marshal batch request: %w", err)
	}

	req, err := client.newHTTPRequest(
		ensureContext(ctx),
		http.MethodPost,
		client.endpoint(BatchPath),
		bytes.NewReader(body),
		jsoncodec.ContentType,
		jsoncodec.ContentType,
	)
	if err != nil {
		return nil, err
	}

	res, err := client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("batch call: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode >= http.StatusBadRequest {
		var rpcRes Response
		if err := decodeHTTPResponse(res, &rpcRes); err != nil {
			return nil, fmt.Errorf("decode batch error response: %w", err)
		}
		if rpcRes.Error != "" || rpcRes.Code != "" {
			return nil, responseError(rpcRes)
		}
		return nil, Errorf(CodeInternal, "batch failed with status %d", res.StatusCode)
	}

	var response batchResponse
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode batch response: %w", err)
	}
	if len(response.Results) != len(calls) {
		return nil, Errorf(CodeInternal, "batch returned %d results for %d calls", len(response.Results), len(calls))
	}

	return response.Results, nil
}

// DecodeBatchResult decodes one successful batch result into Out. Procedure
// errors are returned as structured Neo errors.
func DecodeBatchResult[Out any](result BatchResult) (Out, error) {
	var zero Out
	if result.Error != "" || result.Code != "" {
		return zero, responseError(Response{Code: result.Code, Error: result.Error})
	}
	if len(result.Result) == 0 {
		return zero, nil
	}
	if err := json.Unmarshal(result.Result, &zero); err != nil {
		return zero, fmt.Errorf("decode batch result into %s: %w", typeName[Out](), err)
	}
	return zero, nil
}
