package neo

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, Response{
		Error: message,
	})
}

func readInput(r *http.Request) (any, error) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		rawInput := r.URL.Query().Get("input")
		if rawInput == "" {
			return nil, nil
		}

		var input any
		if err := json.Unmarshal([]byte(rawInput), &input); err != nil {
			return nil, errors.New("invalid input query")
		}

		return input, nil

	case http.MethodPost:
		defer func() {
			_ = r.Body.Close()
		}()

		var req Request
		if err := decodeSingleJSON(r.Body, &req); err != nil {
			return nil, errors.New("invalid JSON body")
		}

		return req.Input, nil

	default:
		return nil, errors.New("method not allowed")
	}
}

func decodeSingleJSON(r io.Reader, value any) error {
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(value); err != nil {
		return err
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}

	return nil
}
