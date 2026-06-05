package neo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestErrorCodeHTTPStatusMapping(t *testing.T) {
	cases := map[ErrorCode]int{
		CodeBadRequest:       http.StatusBadRequest,
		CodeUnauthorized:     http.StatusUnauthorized,
		CodeForbidden:        http.StatusForbidden,
		CodeNotFound:         http.StatusNotFound,
		CodeMethodNotAllowed: http.StatusMethodNotAllowed,
		CodeConflict:         http.StatusConflict,
		CodeInternal:         http.StatusInternalServerError,
		CodeNotImplemented:   http.StatusNotImplemented,
		CodeUnavailable:      http.StatusServiceUnavailable,
		CodeTimeout:          http.StatusGatewayTimeout,
	}
	for code, want := range cases {
		if got := code.HTTPStatus(); got != want {
			t.Fatalf("%s.HTTPStatus() = %d, want %d", code, got, want)
		}
	}
	if got := ErrorCode("UNKNOWN").HTTPStatus(); got != http.StatusInternalServerError {
		t.Fatalf("unknown code status = %d, want 500", got)
	}
}

func TestErrorStringAndUnwrap(t *testing.T) {
	if got := NewError("", "just a message").Error(); got != "just a message" {
		t.Fatalf("codeless error = %q, want just a message", got)
	}
	if got := NewError(CodeNotFound, "missing").Error(); got != "NOT_FOUND: missing" {
		t.Fatalf("coded error = %q, want NOT_FOUND: missing", got)
	}

	cause := errors.New("root cause")
	wrapped := WrapError(CodeConflict, "duplicate", cause)
	if !errors.Is(wrapped, cause) {
		t.Fatal("errors.Is did not find the wrapped cause")
	}
}

func TestProcedureTypedErrorMapsToStatusAndCode(t *testing.T) {
	router := NewRouter()
	router.Register("user.get", Query[testInput, testOutput](func(ctx context.Context, in testInput) (testOutput, error) {
		return testOutput{}, NewError(CodeNotFound, "no such user")
	}))

	server := newTestServer(router)
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
	var body Response
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != string(CodeNotFound) || body.Error != "no such user" {
		t.Fatalf("body = %#v, want NOT_FOUND/no such user", body)
	}

	// Client surfaces a typed *Error so callers can branch on the code.
	client := NewClient(server.URL + "/neo")
	_, callErr := CallTyped[testInput, testOutput](context.Background(), client.Query.Procedure("user.get"), testInput{Name: "x"})
	var typed *Error
	if !errors.As(callErr, &typed) {
		t.Fatalf("error = %v (%T), want *neo.Error", callErr, callErr)
	}
	if typed.Code != CodeNotFound {
		t.Fatalf("code = %q, want NOT_FOUND", typed.Code)
	}
}

func TestPlainErrorDefaultsToInternal(t *testing.T) {
	router := NewRouter()
	router.Register("boom", Query(func(ctx context.Context, input testInput) (testOutput, error) {
		return testOutput{}, errors.New("kaboom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/neo/boom", nil)
	rec := httptest.NewRecorder()

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var got Response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.Code != string(CodeInternal) || got.Error != internalErrorMessage {
		t.Fatalf("body = %#v, want INTERNAL/%q", got, internalErrorMessage)
	}

	if strings.Contains(rec.Body.String(), "kaboom") {
		t.Fatalf("internal error detail leaked: %s", rec.Body.String())
	}
}

func TestMethodEnforcement(t *testing.T) {
	router := NewRouter()
	router.Register("q", Query[testInput, testOutput](func(ctx context.Context, in testInput) (testOutput, error) {
		return testOutput{Message: "query ok"}, nil
	}))
	router.Register("m", Mutation[testInput, testOutput](func(ctx context.Context, in testInput) (testOutput, error) {
		return testOutput{Message: "mutation ok"}, nil
	}))

	server := newTestServer(router)
	defer server.Close()

	// Mutation over GET is rejected.
	res, err := http.Get(server.URL + "/neo/m")
	if err != nil {
		t.Fatalf("GET mutation: %v", err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}

	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("mutation-over-GET status = %d, want 405", res.StatusCode)
	}

	if allow := res.Header.Get("Allow"); allow != "POST, OPTIONS" {
		t.Fatalf("Allow = %q, want POST, OPTIONS", allow)
	}

	// Query over POST is allowed so large query inputs can avoid URL length limits.
	res, err = http.Post(server.URL+"/neo/q", "application/json", strings.NewReader(`{"input":{}}`))
	if err != nil {
		t.Fatalf("POST query: %v", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("query-over-POST status = %d, want 200", res.StatusCode)
	}

	var got Response
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.Error != "" || got.Code != "" {
		t.Fatalf("unexpected error response: %#v", got)
	}
}

// TestClientSubscribeHandlesLargePayload exercises the bufio.Reader path with a
// line well over bufio.MaxScanTokenSize (64 KiB), which the old scanner-based
// implementation would silently drop.
func TestClientSubscribeHandlesLargePayload(t *testing.T) {
	const size = 200 * 1024 // 200 KiB, far past the old 64 KiB cap
	big := strings.Repeat("x", size)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Response{Result: big})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := NewClient(server.URL)
	stream, err := client.Subscription.Procedure("events").Subscribe(ctx, nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	select {
	case value, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before delivering the large payload")
		}
		got, err := decodeClientValue[string](value)
		if err != nil {
			t.Fatalf("decode stream value: %v", err)
		}
		if len(got) != size {
			t.Fatalf("payload length = %d, want %d", len(got), size)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for large payload")
	}
}
