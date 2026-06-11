package jsoncodec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Protocol-Lattice/neo/internal/procedure"
)

type testInput struct {
	Name string `json:"name,omitempty"`
}

type testOutput struct {
	Message string `json:"message,omitempty"`
}

func TestWriteJSONSetsStatusContentTypeAndBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	Write(recorder, http.StatusCreated, Response{Result: testOutput{Message: "ok"}})

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
	WriteError(recorder, http.StatusBadRequest, "bad input")

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
	input, err := ReadInput(req)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}
	if input != nil {
		t.Fatalf("input = %#v, want nil", input)
	}
}

func TestReadInputGETDecodesInputQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, `/neo/hello?input={"name":"Neo"}`, nil)
	input, err := ReadInput(req)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}

	decoded, err := procedure.DecodeInput[testInput](input)
	if err != nil {
		t.Fatalf("decode typed input: %v", err)
	}
	if decoded.Name != "Neo" {
		t.Fatalf("name = %q, want Neo", decoded.Name)
	}
}

func TestReadInputGETDecodesEscapedInputQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, `/neo/hello?trace=1&input=%7B%22name%22%3A%22Neo+Smith%22%7D`, nil)
	input, err := ReadInput(req)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}

	decoded, err := procedure.DecodeInput[testInput](input)
	if err != nil {
		t.Fatalf("decode typed input: %v", err)
	}
	if decoded.Name != "Neo Smith" {
		t.Fatalf("name = %q, want Neo Smith", decoded.Name)
	}
}

func TestReadInputGETRejectsInvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/neo/hello?input=not-json", nil)
	_, err := ReadInput(req)
	if err == nil || err.Error() != "invalid input query" {
		t.Fatalf("error = %v, want invalid input query", err)
	}
}

func TestReadInputGETRejectsInvalidQueryEscape(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/neo/hello?input=%zz", nil)
	_, err := ReadInput(req)
	if err == nil || err.Error() != "invalid input query" {
		t.Fatalf("error = %v, want invalid input query", err)
	}
}

func TestReadInputPOSTDecodesRequestBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/neo/user.create", strings.NewReader(`{"input":{"name":"Kamil"}}`))
	input, err := ReadInput(req)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}

	decoded, err := procedure.DecodeInput[testInput](input)
	if err != nil {
		t.Fatalf("decode typed input: %v", err)
	}
	if decoded.Name != "Kamil" {
		t.Fatalf("name = %q, want Kamil", decoded.Name)
	}
}

func TestReadInputPOSTRejectsInvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/neo/user.create", strings.NewReader(`not-json`))
	_, err := ReadInput(req)
	if err == nil || err.Error() != "invalid json body" {
		t.Fatalf("error = %v, want invalid json body", err)
	}
}

func TestReadInputPOSTRejectsTrailingJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/neo/user.create", strings.NewReader(`{"input":{"name":"Kamil"}} {}`))
	_, err := ReadInput(req)
	if err == nil || err.Error() != "invalid json body" {
		t.Fatalf("error = %v, want invalid json body", err)
	}
}

func TestReadInputRejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/neo/user.create", nil)
	_, err := ReadInput(req)
	if err == nil || err.Error() != "method not allowed" {
		t.Fatalf("error = %v, want method not allowed", err)
	}
}
