package neo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPlainErrorDefaultsToInternalAndRedactsMessage(t *testing.T) {
	oldLogger := ErrorLogger
	ErrorLogger = nil
	t.Cleanup(func() { ErrorLogger = oldLogger })

	router := NewRouter()
	router.Register("boom", Query(func(context.Context, struct{}) (string, error) {
		return "", errors.New("pq: connection refused to 10.0.0.5")
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/neo/boom", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") || strings.Contains(rec.Body.String(), "connection refused") {
		t.Fatalf("internal detail leaked: %s", rec.Body.String())
	}

	var res Response
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Code != string(CodeInternal) || res.Error != internalErrorMessage {
		t.Fatalf("response = %#v, want redacted internal error", res)
	}
}

func TestExplicitErrorMessagePassesThroughForNonInternalCode(t *testing.T) {
	router := NewRouter()
	router.Register("missing", Query(func(context.Context, struct{}) (string, error) {
		return "", NewError(CodeNotFound, "user not found")
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/neo/missing", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "user not found") {
		t.Fatalf("explicit message was not returned: %s", rec.Body.String())
	}
}

func TestOptionsPreflightAndHeadAreAccepted(t *testing.T) {
	router := NewRouter()
	router.Register("ping", Query(func(context.Context, struct{}) (string, error) {
		return "pong", nil
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	preflight := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/neo/ping", nil)
	req.Header.Set("Origin", "https://example.test")
	mux.ServeHTTP(preflight, req)

	if preflight.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want %d", preflight.Code, http.StatusNoContent)
	}
	if got := preflight.Header().Get("Access-Control-Allow-Origin"); got != "https://example.test" {
		t.Fatalf("CORS origin = %q", got)
	}

	head := httptest.NewRecorder()
	mux.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/neo/ping", nil))
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", head.Code, http.StatusOK)
	}
}

func TestQueryAcceptsPostBodyForLargeInputs(t *testing.T) {
	type input struct {
		Value string `json:"value"`
	}

	router := NewRouter()
	router.Register("echo", Query(func(_ context.Context, in input) (int, error) {
		return len(in.Value), nil
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	body := `{"input":{"value":"` + strings.Repeat("x", maxGETInputBytes+1) + `"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/neo/echo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMergeCopiesProceduresSubscriptionsMetadataAndMiddleware(t *testing.T) {
	var calls atomic.Int64
	parent := NewRouter()
	parent.Use(func(next Handler) Handler {
		return func(ctx context.Context, input any) (any, error) {
			calls.Add(1)
			return next(ctx, input)
		}
	})

	child := NewRouter()
	child.Register("ping", Query(func(context.Context, struct{}) (string, error) { return "pong", nil }))
	child.RegisterSubscription("events", Subscription(func(ctx context.Context, _ struct{}) (<-chan string, error) {
		ch := make(chan string, 1)
		ch <- "ok"
		close(ch)
		return ch, nil
	}))

	parent.Merge(child)

	if parent.Method("ping") == nil {
		t.Fatal("merged procedure missing")
	}
	if parent.Subscription("events") == nil {
		t.Fatal("merged subscription missing")
	}
	if len(parent.Metadata()) != 2 {
		t.Fatalf("metadata len = %d, want 2", len(parent.Metadata()))
	}

	_, err := applyMiddlewares(parent.procedureMiddlewares["ping"], func(context.Context, any) (any, error) {
		return nil, nil
	})(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("middleware calls = %d, want 1", calls.Load())
	}
}
