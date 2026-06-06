package neo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewClientInitializesNamespacesAndTrimsAddress(t *testing.T) {
	client := NewClient("http://example.test/neo/")

	if client.addr != "http://example.test/neo" {
		t.Fatalf("addr = %q, want trimmed address", client.addr)
	}
	if client.Query == nil || client.Mutation == nil || client.Subscription == nil {
		t.Fatalf("expected query, mutation, and subscription namespaces to be initialized")
	}
	if client.Query.method != http.MethodGet {
		t.Fatalf("query method = %q, want GET", client.Query.method)
	}
	if client.Mutation.method != http.MethodPost {
		t.Fatalf("mutation method = %q, want POST", client.Mutation.method)
	}
}

func TestClientOptionsConfigureHTTPClientAndHeaders(t *testing.T) {
	customHTTP := &http.Client{Timeout: time.Second}
	client := NewClient(
		"http://example.test/neo",
		WithHTTPClient(customHTTP),
		WithHeader("Authorization", "Bearer token"),
		WithHeaders(http.Header{"X-Trace-ID": []string{"trace-1"}}),
	)

	if client.http != customHTTP {
		t.Fatal("custom HTTP client was not applied")
	}

	req, err := client.newRequest(context.Background(), http.MethodGet, "hello", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("authorization header = %q", got)
	}
	if got := req.Header.Get("X-Trace-ID"); got != "trace-1" {
		t.Fatalf("trace header = %q", got)
	}
}

func TestClientNamespaceProcedureTrimsKey(t *testing.T) {
	client := NewClient("http://example.test/neo")
	procedure := client.Query.Procedure("/user.get/")

	if procedure.key != "user.get" {
		t.Fatalf("procedure key = %q, want user.get", procedure.key)
	}
	if procedure.method != http.MethodGet {
		t.Fatalf("procedure method = %q, want GET", procedure.method)
	}
}

func TestClientNewRequestGETEncodesInputQuery(t *testing.T) {
	client := NewClient("http://example.test/neo")
	req, err := client.newRequest(context.Background(), http.MethodGet, "/hello/", testInput{Name: "Neo"})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if req.Method != http.MethodGet {
		t.Fatalf("method = %q, want GET", req.Method)
	}
	if req.URL.Path != "/neo/hello" {
		t.Fatalf("path = %q, want /neo/hello", req.URL.Path)
	}

	var input testInput
	if err := json.Unmarshal([]byte(req.URL.Query().Get("input")), &input); err != nil {
		t.Fatalf("decode query input: %v", err)
	}
	if input.Name != "Neo" {
		t.Fatalf("input = %#v, want Neo", input)
	}
}

func TestClientNewRequestPOSTEncodesBody(t *testing.T) {
	client := NewClient("http://example.test/neo")
	req, err := client.newRequest(context.Background(), http.MethodPost, "user.create", testInput{Name: "Kamil"})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}

	var body Request
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	decoded, err := decodeClientValue[testInput](body.Input)
	if err != nil {
		t.Fatalf("decode typed body input: %v", err)
	}
	if decoded.Name != "Kamil" {
		t.Fatalf("input = %#v, want Kamil", decoded)
	}
}

func TestEndpointWithInputMatchesQueryEscape(t *testing.T) {
	rawInput := []byte(`{"name":"Neo + Trinity","symbols":"{}[],:/%"}`)
	got := endpointWithInput("http://example.test/neo/hello", rawInput)
	want := "http://example.test/neo/hello?input=" + url.QueryEscape(string(rawInput))

	if got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestClientCallReturnsServerErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusTeapot, "boom")
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Query.Procedure("fail").Call(context.Background(), nil)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("error = %v, want boom", err)
	}
}

func TestClientCallRejectsInvalidResponseJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Query.Procedure("bad").Call(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("error = %v, want decode response error", err)
	}
}

func TestClientMetadataFetchesProcedureMetadata(t *testing.T) {
	router := NewRouter()
	router.Register("hello", Query(func(context.Context, testInput) (testOutput, error) {
		return testOutput{Message: "hi"}, nil
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization header = %q, want Bearer token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("accept = %q, want application/json", got)
		}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL+"/neo", WithHeader("Authorization", "Bearer token"))
	metadata, err := client.Metadata(context.Background())
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if len(metadata) != 1 {
		t.Fatalf("metadata len = %d, want 1", len(metadata))
	}
	if metadata[0].Key != "hello" || metadata[0].Kind != ProcedureKindQuery {
		t.Fatalf("metadata = %#v, want hello query", metadata[0])
	}
}

func TestClientSubscribeReadsNDJSONStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/x-ndjson" {
			t.Fatalf("accept = %q, want application/x-ndjson", got)
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Response{Result: testOutput{Message: "one"}})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := NewClient(server.URL)
	stream, err := client.Subscription.Procedure("events").Subscribe(ctx, nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	select {
	case value, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before first value")
		}
		got, err := decodeClientValue[testOutput](value)
		if err != nil {
			t.Fatalf("decode stream value: %v", err)
		}
		if got.Message != "one" {
			t.Fatalf("message = %q, want one", got.Message)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for stream value")
	}
}

func TestClientSubscribeAcceptsNilContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Response{Result: testOutput{Message: "one"}})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	stream, err := client.Subscription.Procedure("events").Subscribe(nil, nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	select {
	case value, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before first value")
		}
		got, err := decodeClientValue[testOutput](value)
		if err != nil {
			t.Fatalf("decode stream value: %v", err)
		}
		if got.Message != "one" {
			t.Fatalf("message = %q, want one", got.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream value")
	}
}

func TestDecodeClientStreamStopsOnDecodeError(t *testing.T) {
	raw := make(chan any, 2)
	raw <- map[string]any{"message": map[string]any{"nested": true}}
	raw <- testOutput{Message: "unreachable"}
	close(raw)

	stream := decodeClientStream[testOutput](context.Background(), raw)

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("stream is open, want closed after decode error")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream to close")
	}
}

func TestDecodeClientStreamClosesNilStream(t *testing.T) {
	stream := decodeClientStream[testOutput](context.TODO(), nil)

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("stream is open, want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for nil stream to close")
	}
}

func TestDecodeClientStreamStopsWaitingOnContextCancel(t *testing.T) {
	raw := make(chan any)
	ctx, cancel := context.WithCancel(context.Background())

	stream := decodeClientStream[testOutput](ctx, raw)
	cancel()

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("stream is open, want closed after context cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled stream to close")
	}
}
