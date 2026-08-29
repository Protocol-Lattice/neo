package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	routerruntime "github.com/Protocol-Lattice/neo/internal/router"
)

func TestClientBatchReturnsOrderedTypedResultsAndProcedureErrors(t *testing.T) {
	router := routerruntime.NewRouter()
	router.Register("hello", routerruntime.Query(func(_ context.Context, in testInput) (testOutput, error) {
		return testOutput{Message: "hello " + in.Name}, nil
	}))
	router.Register("fail", routerruntime.Mutation(func(context.Context, testInput) (testOutput, error) {
		return testOutput{}, NewError(CodeBadRequest, "invalid user")
	}))
	router.Register("create", routerruntime.Mutation(func(_ context.Context, in testInput) (testOutput, error) {
		return testOutput{Message: "created " + in.Name}, nil
	}))

	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/neo/_batch" {
			t.Fatalf("path = %q, want batch endpoint", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL+"/neo", WithBinaryCodec())
	results, err := client.Batch(context.Background(),
		BatchCall{Procedure: client.Query.Procedure("hello"), Input: testInput{Name: "Ada"}},
		BatchCall{Procedure: client.Mutation.Procedure("fail"), Input: testInput{Name: "Ada"}},
		BatchCall{Procedure: client.Mutation.Procedure("create"), Input: testInput{Name: "Neo"}},
	)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %#v, want three", results)
	}

	first, err := DecodeBatchResult[testOutput](results[0])
	if err != nil {
		t.Fatalf("decode first result: %v", err)
	}
	if first.Message != "hello Ada" {
		t.Fatalf("first = %#v, want hello Ada", first)
	}

	_, err = DecodeBatchResult[testOutput](results[1])
	if err == nil {
		t.Fatal("decode second result = nil, want procedure error")
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("second error = %T, want *Error", err)
	}
	if rpcErr.Code != CodeBadRequest || rpcErr.Message != "invalid user" {
		t.Fatalf("second error = %#v, want BAD_REQUEST invalid user", rpcErr)
	}

	third, err := DecodeBatchResult[testOutput](results[2])
	if err != nil {
		t.Fatalf("decode third result: %v", err)
	}
	if third.Message != "created Neo" {
		t.Fatalf("third = %#v, want created Neo", third)
	}
}

func TestClientBatchRejectsForeignAndNilProceduresWithoutRequest(t *testing.T) {
	client := NewClient("http://example.test/neo")
	other := NewClient("http://example.test/neo")

	_, err := client.Batch(context.Background(), BatchCall{Procedure: nil})
	if err == nil {
		t.Fatal("nil procedure error = nil")
	}

	_, err = client.Batch(context.Background(), BatchCall{Procedure: other.Query.Procedure("hello")})
	if err == nil {
		t.Fatal("foreign procedure error = nil")
	}

	results, err := client.Batch(context.Background())
	if err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("empty results = %#v, want empty", results)
	}
}
