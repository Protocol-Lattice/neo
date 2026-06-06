package neo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONSetsStatusContentTypeAndBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeJSON(recorder, http.StatusCreated, Response{Result: testOutput{Message: "ok"}})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}

	var res Response
	if err := json.NewDecoder(recorder.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Result == nil {
		t.Fatalf("expected result body, got %#v", res)
	}
}

func TestWriteErrorEncodesErrorResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeError(recorder, http.StatusBadRequest, "bad input")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	var res Response
	if err := json.NewDecoder(recorder.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Error != "bad input" {
		t.Fatalf("error = %q, want bad input", res.Error)
	}
}

func TestReadInputGETEmptyReturnsNil(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/neo/hello", nil)
	input, err := readInput(req)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}
	if input != nil {
		t.Fatalf("input = %#v, want nil", input)
	}
}

func TestReadInputGETDecodesInputQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, `/neo/hello?input={"name":"Neo"}`, nil)
	input, err := readInput(req)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}

	decoded, err := decodeInput[testInput](input)
	if err != nil {
		t.Fatalf("decode typed input: %v", err)
	}
	if decoded.Name != "Neo" {
		t.Fatalf("name = %q, want Neo", decoded.Name)
	}
}

func TestReadInputGETRejectsInvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/neo/hello?input=not-json", nil)
	_, err := readInput(req)
	if err == nil || err.Error() != "invalid input query" {
		t.Fatalf("error = %v, want invalid input query", err)
	}
}

func TestReadInputPOSTDecodesRequestBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/neo/user.create", strings.NewReader(`{"input":{"name":"Kamil"}}`))
	input, err := readInput(req)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}

	decoded, err := decodeInput[testInput](input)
	if err != nil {
		t.Fatalf("decode typed input: %v", err)
	}
	if decoded.Name != "Kamil" {
		t.Fatalf("name = %q, want Kamil", decoded.Name)
	}
}

func TestReadInputPOSTRejectsInvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/neo/user.create", strings.NewReader(`not-json`))
	_, err := readInput(req)
	if err == nil || err.Error() != "invalid json body" {
		t.Fatalf("error = %v, want invalid json body", err)
	}
}

func TestReadInputPOSTRejectsTrailingJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/neo/user.create", strings.NewReader(`{"input":{"name":"Kamil"}} {}`))
	_, err := readInput(req)
	if err == nil || err.Error() != "invalid json body" {
		t.Fatalf("error = %v, want invalid json body", err)
	}
}

func TestReadInputRejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/neo/user.create", nil)
	_, err := readInput(req)
	if err == nil || err.Error() != "method not allowed" {
		t.Fatalf("error = %v, want method not allowed", err)
	}
}
