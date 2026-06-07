package neo

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	jsonContentType = "application/json"
	ndjsonMediaType = "application/x-ndjson"
)

type rawRequest struct {
	Input json.RawMessage `json:"input"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(value)
}

func writeResponse(w http.ResponseWriter, r *http.Request, status int, value any) error {
	if acceptsContentType(r.Header.Get("Accept"), BinaryContentType) {
		raw, err := NeoBinaryCodec.Marshal(value)
		if err != nil {
			return err
		}

		w.Header().Set("Content-Type", BinaryContentType)
		w.WriteHeader(status)
		_, err = w.Write(raw)
		return err
	}

	writeJSON(w, status, value)
	return nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, Response{
		Error: message,
	})
}

func readInput(r *http.Request) (any, error) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		input, err := readQueryInput(r.URL.RawQuery)
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

		if isContentType(r.Header.Get("Content-Type"), BinaryContentType) {
			var req Request
			if err := decodeSingleBinary(r.Body, &req); err != nil {
				return nil, errors.New("invalid binary body")
			}
			return req.Input, nil
		}

		var req rawRequest
		if err := decodeSingleJSON(r.Body, &req); err != nil {
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

func readQueryInput(rawQuery string) (json.RawMessage, error) {
	for rawQuery != "" {
		param := rawQuery
		if before, after, ok := strings.Cut(rawQuery, "&"); ok {
			param = before
			rawQuery = after
		} else {
			rawQuery = ""
		}

		key, value, hasValue := strings.Cut(param, "=")
		if key != "input" {
			continue
		}
		if !hasValue || value == "" {
			return nil, nil
		}

		return queryUnescapeBytes(value)
	}

	return nil, nil
}

func queryUnescapeBytes(value string) (json.RawMessage, error) {
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '+', '%':
			return appendQueryUnescaped(make([]byte, 0, len(value)), value)
		}
	}

	return json.RawMessage(value), nil
}

func appendQueryUnescaped(dst []byte, value string) (json.RawMessage, error) {
	for i := 0; i < len(value); i++ {
		switch ch := value[i]; ch {
		case '+':
			dst = append(dst, ' ')
		case '%':
			if i+2 >= len(value) {
				return nil, errors.New("invalid query escape")
			}

			hi, ok := fromHex(value[i+1])
			if !ok {
				return nil, errors.New("invalid query escape")
			}
			lo, ok := fromHex(value[i+2])
			if !ok {
				return nil, errors.New("invalid query escape")
			}

			dst = append(dst, hi<<4|lo)
			i += 2
		default:
			dst = append(dst, ch)
		}
	}

	return dst, nil
}

func fromHex(ch byte) (byte, bool) {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0', true
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10, true
	case ch >= 'A' && ch <= 'F':
		return ch - 'A' + 10, true
	default:
		return 0, false
	}
}

func decodeSingleJSON(r io.Reader, value any) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	return json.Unmarshal(raw, value)
}

func decodeSingleBinary(r io.Reader, value any) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	return NeoBinaryCodec.Unmarshal(raw, value)
}

func isContentType(header string, contentType string) bool {
	mediaType, _, _ := strings.Cut(header, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), contentType)
}

func acceptsContentType(header string, contentType string) bool {
	for header != "" {
		var part string
		part, header, _ = strings.Cut(header, ",")
		mediaType, _, _ := strings.Cut(part, ";")
		if strings.EqualFold(strings.TrimSpace(mediaType), contentType) {
			return true
		}
	}
	return false
}
