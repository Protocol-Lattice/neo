package router

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type batchRequest struct {
	Calls []batchCall `json:"calls"`
}

type batchCall struct {
	Key   string          `json:"key"`
	Input json.RawMessage `json:"input"`
}

type batchResponse struct {
	Results []batchResult `json:"results"`
}

type batchResult struct {
	Result json.RawMessage `json:"result,omitempty"`
	Code   string          `json:"code,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// serveBatch executes calls in request order. Procedures share the request
// context but keep their own middleware and observation context. Subscriptions
// are excluded because they require a streaming response.
func (router *Router) serveBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeProcedureError(w, NewError(CodeMethodNotAllowed, "batch calls require POST"))
		return
	}

	request, err := readBatchRequest(r)
	if err != nil {
		writeProcedureError(w, WrapError(CodeBadRequest, err.Error(), err))
		return
	}

	results := make([]batchResult, 0, len(request.Calls))
	for _, call := range request.Calls {
		key := strings.Trim(call.Key, ".")
		if router.subscriptions[key] != nil {
			results = append(results, batchErrorResult(NewError(CodeMethodNotAllowed, "subscriptions are not supported in batch calls")))
			continue
		}
		procedure := router.procedures[key]
		if procedure == nil {
			results = append(results, batchErrorResult(NewError(CodeNotFound, "procedure not found")))
			continue
		}
		if procedure.Kind != ProcedureKindQuery && procedure.Kind != ProcedureKindMutation {
			results = append(results, batchErrorResult(NewError(CodeMethodNotAllowed, "only queries and mutations are supported in batch calls")))
			continue
		}

		output, err := callProcedure(r.Context(), key, procedure, router.procedureMiddlewares[key], call.Input)
		if err != nil {
			results = append(results, batchErrorResult(err))
			continue
		}

		result, err := json.Marshal(output)
		if err != nil {
			results = append(results, batchErrorResult(WrapError(CodeInternal, "encode response", err)))
			continue
		}
		results = append(results, batchResult{Result: result})
	}

	writeJSON(w, http.StatusOK, batchResponse{Results: results})
}

func batchErrorResult(err error) batchResult {
	_, response := procedureErrorResponse(err)
	return batchResult{Code: response.Code, Error: response.Error}
}

func readBatchRequest(r *http.Request) (batchRequest, error) {
	var request batchRequest
	contentType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if contentType != "" && !strings.EqualFold(contentType, "application/json") {
		return request, errors.New("batch calls require application/json")
	}
	defer func() {
		_ = r.Body.Close()
	}()

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		return request, errors.New("invalid json body")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return request, errors.New("invalid json body")
	}
	if request.Calls == nil {
		return request, errors.New("batch calls are required")
	}

	return request, nil
}
