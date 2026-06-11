package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	routest "github.com/Protocol-Lattice/neo/internal/tests"
)

func TestPlainErrorDefaultsToInternalAndRedactsMessage(t *testing.T) {
	router := routest.NewRouter()
	router.Register("boom", routest.Query(func(context.Context, struct{}) (string, error) {
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

	var res routest.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Code != string(routest.CodeInternal) || res.Error != routest.InternalErrorMessage {
		t.Fatalf("response = %#v, want redacted internal error", res)
	}
}

func TestExplicitErrorMessagePassesThroughForNonInternalCode(t *testing.T) {
	router := routest.NewRouter()
	router.Register("missing", routest.Query(func(context.Context, struct{}) (string, error) {
		return "", routest.NewError(routest.CodeNotFound, "user not found")
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
	router := routest.NewRouter()
	router.Register("ping", routest.Query(func(context.Context, struct{}) (string, error) {
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

	router := routest.NewRouter()
	router.Register("echo", routest.Query(func(_ context.Context, in input) (int, error) {
		return len(in.Value), nil
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	body := `{"input":{"value":"` + strings.Repeat("x", routest.LargeGETInputBytes+1) + `"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/neo/echo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMethodEnforcement(t *testing.T) {
	router := routest.NewRouter()
	router.Register("q", routest.Query[routest.Input, routest.Output](func(ctx context.Context, in routest.Input) (routest.Output, error) {
		return routest.Output{Message: "query ok"}, nil
	}))
	router.Register("m", routest.Mutation[routest.Input, routest.Output](func(ctx context.Context, in routest.Input) (routest.Output, error) {
		return routest.Output{Message: "mutation ok"}, nil
	}))

	server := routest.NewTestServer(router)
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

	var got routest.Response
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.Error != "" || got.Code != "" {
		t.Fatalf("unexpected error response: %#v", got)
	}
}

func TestMergeCopiesProceduresSubscriptionsMetadataAndMiddleware(t *testing.T) {
	var calls atomic.Int64
	parent := routest.NewRouter()
	parent.Use(func(next routest.Handler) routest.Handler {
		return func(ctx context.Context, input any) (any, error) {
			calls.Add(1)
			return next(ctx, input)
		}
	})

	child := routest.NewRouter()
	child.Register("ping", routest.Query(func(context.Context, struct{}) (string, error) { return "pong", nil }))
	child.RegisterSubscription("events", routest.Subscription(func(ctx context.Context, _ struct{}) (<-chan string, error) {
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

	mux := http.NewServeMux()
	parent.ServeHTTP(mux, "/neo/")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/neo/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("middleware calls = %d, want 1", calls.Load())
	}
}

func TestServerOptionsDefaultsNonPositiveMaxRequestBody(t *testing.T) {
	opts := routest.ServerOptionsWithDefaults(routest.ServerOptions{MaxRequestBody: -1})
	if opts.MaxRequestBody != routest.DefaultMaxRequestBody {
		t.Fatalf("MaxRequestBody = %d, want %d", opts.MaxRequestBody, routest.DefaultMaxRequestBody)
	}
}

func TestUseCORSRestrictsOriginsAndCredentials(t *testing.T) {
	router := routest.NewRouter()
	router.UseCORS(routest.CORSOptions{
		AllowedOrigins:   []string{"https://app.example"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-Trace-ID"},
		AllowCredentials: true,
	})
	router.Register("ping", routest.Query(func(context.Context, struct{}) (string, error) { return "pong", nil }))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodOptions, "/neo/ping", nil)
	allowedReq.Header.Set("Origin", "https://app.example")
	mux.ServeHTTP(allowed, allowedReq)

	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("allowed origin = %q", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Trace-ID") {
		t.Fatalf("allow headers = %q", got)
	}

	blocked := httptest.NewRecorder()
	blockedReq := httptest.NewRequest(http.MethodOptions, "/neo/ping", nil)
	blockedReq.Header.Set("Origin", "https://evil.example")
	mux.ServeHTTP(blocked, blockedReq)

	if got := blocked.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("blocked origin header = %q, want empty", got)
	}
}
