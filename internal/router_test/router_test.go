package router_test

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

	body := `{"input":{"value":"` + strings.Repeat("x", largeGETInputBytes+1) + `"}}`
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

func TestMetadataReturnsStableKeyOrder(t *testing.T) {
	router := NewRouter()
	router.Register("zeta", Query(func(context.Context, struct{}) (string, error) { return "", nil }))
	router.Register("alpha", Query(func(context.Context, struct{}) (string, error) { return "", nil }))
	router.RegisterSubscription("events", Subscription(func(context.Context, struct{}) (<-chan string, error) {
		ch := make(chan string)
		close(ch)
		return ch, nil
	}))

	metas := router.Metadata()
	got := make([]string, 0, len(metas))
	for _, meta := range metas {
		got = append(got, meta.Key)
	}

	want := []string{"alpha", "events", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("metadata keys = %#v, want %#v", got, want)
	}
}

func TestMetadataEndpointReturnsProcedureMetadata(t *testing.T) {
	router := NewRouter()
	router.Register("zeta", Query(func(context.Context, struct{}) (string, error) { return "", nil }))
	router.Register("alpha", Mutation(func(context.Context, testInput) (testOutput, error) {
		return testOutput{}, nil
	}))
	router.RegisterSubscription("events", Subscription(func(context.Context, struct{}) (<-chan string, error) {
		ch := make(chan string)
		close(ch)
		return ch, nil
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/neo/"+MetadataPath, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var metadata []ProcedureMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if len(metadata) != 3 {
		t.Fatalf("metadata len = %d, want 3", len(metadata))
	}

	got := []string{metadata[0].Key, metadata[1].Key, metadata[2].Key}
	want := []string{"alpha", "events", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("metadata keys = %#v, want %#v", got, want)
	}
	if metadata[0].Kind != ProcedureKindMutation {
		t.Fatalf("alpha kind = %q, want mutation", metadata[0].Kind)
	}
}

func TestMetadataEndpointRejectsUnsupportedMethods(t *testing.T) {
	router := NewRouter()
	router.Register("ping", Query(func(context.Context, struct{}) (string, error) { return "pong", nil }))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/neo/"+MetadataPath, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD, OPTIONS" {
		t.Fatalf("allow = %q, want GET, HEAD, OPTIONS", got)
	}
}

func TestNilRegistrationRemovesMetadata(t *testing.T) {
	router := NewRouter()
	router.Register("ping", Query(func(context.Context, struct{}) (string, error) {
		return "pong", nil
	}))
	router.Register("ping", nil)

	if router.Method("ping") != nil {
		t.Fatal("method = non-nil, want nil")
	}
	if got := router.Metadata(); len(got) != 0 {
		t.Fatalf("metadata = %#v, want empty", got)
	}

	router.RegisterSubscription("events", Subscription(func(context.Context, struct{}) (<-chan string, error) {
		ch := make(chan string)
		close(ch)
		return ch, nil
	}))
	router.RegisterSubscription("events", nil)

	if router.Subscription("events") != nil {
		t.Fatal("subscription = non-nil, want nil")
	}
	if got := router.Metadata(); len(got) != 0 {
		t.Fatalf("metadata = %#v, want empty", got)
	}
}

func TestServerOptionsDefaultsNonPositiveMaxRequestBody(t *testing.T) {
	opts := ServerOptionsWithDefaults(ServerOptions{MaxRequestBody: -1})
	if opts.MaxRequestBody != DefaultMaxRequestBody {
		t.Fatalf("MaxRequestBody = %d, want %d", opts.MaxRequestBody, DefaultMaxRequestBody)
	}
}

func TestUseCORSRestrictsOriginsAndCredentials(t *testing.T) {
	router := NewRouter()
	router.UseCORS(CORSOptions{
		AllowedOrigins:   []string{"https://app.example"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-Trace-ID"},
		AllowCredentials: true,
	})
	router.Register("ping", Query(func(context.Context, struct{}) (string, error) { return "pong", nil }))

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
