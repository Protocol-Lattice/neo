package jsoncodec

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	binarycodec "github.com/Protocol-Lattice/neo/internal/codec/binary"
	httptransport "github.com/Protocol-Lattice/neo/internal/transport/http"
)

const (
	ContentType       = "application/json"
	NDJSONContentType = "application/x-ndjson"
	BinaryContentType = binarycodec.ContentType
)

type Request struct {
	Input any `json:"input"`
}

type Response struct {
	Result any    `json:"result,omitempty"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

type rawRequest struct {
	Input json.RawMessage `json:"input"`
}

func Write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(value)
}

func WriteResponse(w http.ResponseWriter, r *http.Request, status int, value any) error {
	if httptransport.AcceptsContentType(r.Header.Get("Accept"), BinaryContentType) {
		raw, err := binarycodec.Default.Marshal(value)
		if err != nil {
			return err
		}

		w.Header().Set("Content-Type", BinaryContentType)
		w.WriteHeader(status)
		_, err = w.Write(raw)
		return err
	}

	Write(w, status, value)
	return nil
}

func WriteError(w http.ResponseWriter, status int, message string) {
	Write(w, status, Response{
		Error: message,
	})
}

func ReadInput(r *http.Request) (any, error) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		input, err := httptransport.ReadQueryInput(r.URL.RawQuery)
		if err != nil {
			return nil, errors.New("invalid input query")
		}
		if len(input) == 0 {
			return nil, nil
		}

		if !json.Valid(input) {
			return nil, errors.New("invalid input query")
		}

		return input, nil

	case http.MethodPost:
		defer func() {
			_ = r.Body.Close()
		}()

		if httptransport.IsContentType(r.Header.Get("Content-Type"), BinaryContentType) {
			var req Request
			if err := DecodeSingleBinary(r.Body, &req); err != nil {
				return nil, errors.New("invalid binary body")
			}
			return req.Input, nil
		}

		var req rawRequest
		if err := DecodeSingle(r.Body, &req); err != nil {
			return nil, errors.New("invalid json body")
		}
		if len(req.Input) == 0 {
			return nil, nil
		}

		return req.Input, nil

	default:
		return nil, errors.New("method not allowed")
	}
}

func DecodeSingle(r io.Reader, value any) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	return json.Unmarshal(raw, value)
}

func DecodeSingleBinary(r io.Reader, value any) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	return binarycodec.Default.Unmarshal(raw, value)
}
