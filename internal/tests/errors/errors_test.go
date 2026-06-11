package router_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clientruntime "github.com/Protocol-Lattice/neo/internal/client"
	routest "github.com/Protocol-Lattice/neo/internal/tests"
)

func TestErrorCodeHTTPStatusMapping(t *testing.T) {
	cases := map[routest.ErrorCode]int{
		routest.CodeBadRequest:       http.StatusBadRequest,
		routest.CodeUnauthorized:     http.StatusUnauthorized,
		routest.CodeForbidden:        http.StatusForbidden,
		routest.CodeNotFound:         http.StatusNotFound,
		routest.CodeMethodNotAllowed: http.StatusMethodNotAllowed,
		routest.CodeConflict:         http.StatusConflict,
		routest.CodeInternal:         http.StatusInternalServerError,
		routest.CodeNotImplemented:   http.StatusNotImplemented,
		routest.CodeUnavailable:      http.StatusServiceUnavailable,
		routest.CodeTimeout:          http.StatusGatewayTimeout,
	}
	for code, want := range cases {
		if got := code.HTTPStatus(); got != want {
			t.Fatalf("%s.HTTPStatus() = %d, want %d", code, got, want)
		}
	}
	if got := routest.ErrorCode("UNKNOWN").HTTPStatus(); got != http.StatusInternalServerError {
		t.Fatalf("unknown code status = %d, want 500", got)
	}
}

func TestErrorStringAndUnwrap(t *testing.T) {
	if got := routest.NewError("", "just a message").Error(); got != "just a message" {
		t.Fatalf("codeless error = %q, want just a message", got)
	}
	if got := routest.NewError(routest.CodeNotFound, "missing").Error(); got != "NOT_FOUND: missing" {
		t.Fatalf("coded error = %q, want NOT_FOUND: missing", got)
	}

	cause := errors.New("root cause")
	wrapped := routest.WrapError(routest.CodeConflict, "duplicate", cause)
	if !errors.Is(wrapped, cause) {
		t.Fatal("errors.Is did not find the wrapped cause")
	}
}

func TestProcedureTypedErrorMapsToStatusAndCode(t *testing.T) {
	router := routest.NewRouter()
	router.Register("user.get", routest.Query[routest.Input, routest.Output](func(ctx context.Context, in routest.Input) (routest.Output, error) {
		return routest.Output{}, routest.NewError(routest.CodeNotFound, "no such user")
	}))

	server := routest.NewTestServer(router)
	defer server.Close()

	// HTTP status reflects the code.
	res, err := http.Get(server.URL + "/neo/user.get")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	var body routest.Response
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != string(routest.CodeNotFound) || body.Error != "no such user" {
		t.Fatalf("body = %#v, want NOT_FOUND/no such user", body)
	}

	// Client surfaces a typed *Error so callers can branch on the code.
	client := clientruntime.NewClient(server.URL + "/neo")
	_, callErr := clientruntime.CallTyped[routest.Input, routest.Output](context.Background(), client.Query.Procedure("user.get"), routest.Input{Name: "x"})
	var typed *routest.Error
	if !errors.As(callErr, &typed) {
		t.Fatalf("error = %v (%T), want *neo.Error", callErr, callErr)
	}
	if typed.Code != routest.CodeNotFound {
		t.Fatalf("code = %q, want NOT_FOUND", typed.Code)
	}
}

func TestPlainErrorDefaultsToInternal(t *testing.T) {
	router := routest.NewRouter()
	router.Register("boom", routest.Query(func(ctx context.Context, input routest.Input) (routest.Output, error) {
		return routest.Output{}, errors.New("kaboom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/neo/boom", nil)
	rec := httptest.NewRecorder()

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var got routest.Response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.Code != string(routest.CodeInternal) || got.Error != routest.InternalErrorMessage {
		t.Fatalf("body = %#v, want INTERNAL/%q", got, routest.InternalErrorMessage)
	}

	if strings.Contains(rec.Body.String(), "kaboom") {
		t.Fatalf("internal error detail leaked: %s", rec.Body.String())
	}
}
