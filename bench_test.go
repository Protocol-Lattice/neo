package neo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type benchInput struct {
	A int `json:"a"`
	B int `json:"b"`
}

type benchOutput struct {
	Sum int `json:"sum"`
}

func newBenchmarkNeoClient(b *testing.B, kind ProcedureKind) *ClientProcedure {
	b.Helper()

	router := NewRouter()
	fn := func(ctx context.Context, input benchInput) (benchOutput, error) {
		return benchOutput{Sum: input.A + input.B}, nil
	}

	if kind == ProcedureKindMutation {
		router.Register("sum", Mutation[benchInput, benchOutput](fn))
	} else {
		router.Register("sum", Query[benchInput, benchOutput](fn))
	}

	server := newTestServer(router)
	b.Cleanup(server.Close)

	client := NewClient(server.URL + "/neo")
	if kind == ProcedureKindMutation {
		return client.Mutation.Procedure("sum")
	}
	return client.Query.Procedure("sum")
}

func BenchmarkNeoQueryHTTPServer(b *testing.B) {
	procedure := newBenchmarkNeoClient(b, ProcedureKindQuery)
	ctx := context.Background()
	input := benchInput{A: 40, B: 2}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		got, err := CallTyped[benchInput, benchOutput](ctx, procedure, input)
		if err != nil {
			b.Fatal(err)
		}
		if got.Sum != 42 {
			b.Fatalf("sum = %d, want 42", got.Sum)
		}
	}
}

func BenchmarkPlainQueryHTTPServer(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input benchInput
		if err := json.Unmarshal([]byte(r.URL.Query().Get("input")), &input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Result: benchOutput{Sum: input.A + input.B}})
	}))
	b.Cleanup(server.Close)

	client := http.DefaultClient
	input := benchInput{A: 40, B: 2}
	rawInput, err := json.Marshal(input)
	if err != nil {
		b.Fatal(err)
	}
	endpoint := server.URL + "/sum?input=" + url.QueryEscape(string(rawInput))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
		if err != nil {
			b.Fatal(err)
		}

		res, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}

		var rpcRes Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			res.Body.Close()
			b.Fatal(err)
		}
		res.Body.Close()

		if res.StatusCode != http.StatusOK {
			b.Fatalf("status = %d", res.StatusCode)
		}
	}
}

func BenchmarkNeoMutationHTTPServer(b *testing.B) {
	procedure := newBenchmarkNeoClient(b, ProcedureKindMutation)
	ctx := context.Background()
	input := benchInput{A: 40, B: 2}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		got, err := CallTyped[benchInput, benchOutput](ctx, procedure, input)
		if err != nil {
			b.Fatal(err)
		}
		if got.Sum != 42 {
			b.Fatalf("sum = %d, want 42", got.Sum)
		}
	}
}

func BenchmarkPlainMutationHTTPServer(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		rawInput, err := json.Marshal(req.Input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var input benchInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Result: benchOutput{Sum: input.A + input.B}})
	}))
	b.Cleanup(server.Close)

	client := http.DefaultClient
	input := Request{Input: benchInput{A: 40, B: 2}}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rawBody, err := json.Marshal(input)
		if err != nil {
			b.Fatal(err)
		}

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/sum", bytes.NewReader(rawBody))
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")

		res, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}

		var rpcRes Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			res.Body.Close()
			b.Fatal(err)
		}
		res.Body.Close()

		if res.StatusCode != http.StatusOK {
			b.Fatalf("status = %d", res.StatusCode)
		}
	}
}
