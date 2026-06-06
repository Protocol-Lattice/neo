package benchmarks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	neo "github.com/Protocol-Lattice/neo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/test/bufconn"
)

type benchInput struct {
	A int `json:"a"`
	B int `json:"b"`
}

type benchOutput struct {
	Sum int `json:"sum"`
}

type benchGRPCServer struct {
	grpc_testing.UnimplementedBenchmarkServiceServer
}

func (benchGRPCServer) UnaryCall(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	return &grpc_testing.SimpleResponse{
		Payload: &grpc_testing.Payload{
			Type: grpc_testing.PayloadType_COMPRESSABLE,
			Body: []byte("42"),
		},
	}, nil
}

func newBenchmarkNeoRouter(kind neo.ProcedureKind) *neo.Router {
	router := neo.NewRouter()
	fn := func(ctx context.Context, input benchInput) (benchOutput, error) {
		return benchOutput{Sum: input.A + input.B}, nil
	}

	if kind == neo.ProcedureKindMutation {
		router.Register("sum", neo.Mutation[benchInput, benchOutput](fn))
	} else {
		router.Register("sum", neo.Query[benchInput, benchOutput](fn))
	}

	return router
}

func newBenchmarkNeoClient(b *testing.B, kind neo.ProcedureKind) *neo.ClientProcedure {
	b.Helper()

	router := newBenchmarkNeoRouter(kind)
	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	server := httptest.NewServer(mux)
	b.Cleanup(server.Close)

	client := neo.NewClient(server.URL + "/neo")
	if kind == neo.ProcedureKindMutation {
		return client.Mutation.Procedure("sum")
	}
	return client.Query.Procedure("sum")
}

func newBenchmarkNeoBufConnClient(b *testing.B, kind neo.ProcedureKind) (*neo.ClientProcedure, func()) {
	b.Helper()

	listener := bufconn.Listen(1 << 20)
	mux := http.NewServeMux()
	newBenchmarkNeoRouter(kind).ServeHTTP(mux, "/neo/")

	server := &http.Server{Handler: mux}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		},
	}

	cleanup := func() {
		transport.CloseIdleConnections()
		if err := server.Close(); err != nil {
			b.Logf("neo bufconn benchmark server close: %v", err)
		}
		_ = listener.Close()

		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			b.Logf("neo bufconn benchmark server stopped: %v", err)
		}
	}

	client := neo.NewClient("http://bufconn/neo", neo.WithHTTPClient(&http.Client{Transport: transport}))
	if kind == neo.ProcedureKindMutation {
		return client.Mutation.Procedure("sum"), cleanup
	}
	return client.Query.Procedure("sum"), cleanup
}

func newBenchmarkGRPCClient(b *testing.B) (grpc_testing.BenchmarkServiceClient, *grpc_testing.SimpleRequest) {
	b.Helper()

	const bufferSize = 1 << 20

	listener := bufconn.Listen(bufferSize)
	server := grpc.NewServer()
	grpc_testing.RegisterBenchmarkServiceServer(server, benchGRPCServer{})

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	b.Cleanup(func() {
		server.Stop()
		_ = listener.Close()

		if err := <-serveErr; err != nil {
			b.Logf("grpc benchmark server stopped: %v", err)
		}
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = conn.Close()
	})

	request := &grpc_testing.SimpleRequest{
		Payload: &grpc_testing.Payload{
			Type: grpc_testing.PayloadType_COMPRESSABLE,
			Body: []byte(`{"a":40,"b":2}`),
		},
	}

	return grpc_testing.NewBenchmarkServiceClient(conn), request
}

func newBenchmarkGRPCTCPClient(b *testing.B) (grpc_testing.BenchmarkServiceClient, *grpc_testing.SimpleRequest) {
	b.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}

	server := grpc.NewServer()
	grpc_testing.RegisterBenchmarkServiceServer(server, benchGRPCServer{})

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	b.Cleanup(func() {
		server.Stop()
		_ = listener.Close()

		if err := <-serveErr; err != nil {
			b.Logf("grpc benchmark server stopped: %v", err)
		}
	})

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = conn.Close()
	})

	request := &grpc_testing.SimpleRequest{
		Payload: &grpc_testing.Payload{
			Type: grpc_testing.PayloadType_COMPRESSABLE,
			Body: []byte(`{"a":40,"b":2}`),
		},
	}

	return grpc_testing.NewBenchmarkServiceClient(conn), request
}

func BenchmarkNeoQueryHTTPServer(b *testing.B) {
	procedure := newBenchmarkNeoClient(b, neo.ProcedureKindQuery)
	ctx := context.Background()
	input := benchInput{A: 40, B: 2}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		got, err := neo.CallTyped[benchInput, benchOutput](ctx, procedure, input)
		if err != nil {
			b.Fatal(err)
		}
		if got.Sum != 42 {
			b.Fatalf("sum = %d, want 42", got.Sum)
		}
	}
}

func BenchmarkNeoQueryBufConn(b *testing.B) {
	procedure, cleanup := newBenchmarkNeoBufConnClient(b, neo.ProcedureKindQuery)
	defer cleanup()

	ctx := context.Background()
	input := benchInput{A: 40, B: 2}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		got, err := neo.CallTyped[benchInput, benchOutput](ctx, procedure, input)
		if err != nil {
			b.Fatal(err)
		}
		if got.Sum != 42 {
			b.Fatalf("sum = %d, want 42", got.Sum)
		}
	}
}

func BenchmarkGRPCUnaryBufConn(b *testing.B) {
	client, input := newBenchmarkGRPCClient(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		got, err := client.UnaryCall(ctx, input)
		if err != nil {
			b.Fatal(err)
		}
		if string(got.GetPayload().GetBody()) != "42" {
			b.Fatalf("payload = %q, want 42", got.GetPayload().GetBody())
		}
	}
}

func BenchmarkGRPCUnaryTCPServer(b *testing.B) {
	client, input := newBenchmarkGRPCTCPClient(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		got, err := client.UnaryCall(ctx, input)
		if err != nil {
			b.Fatal(err)
		}
		if string(got.GetPayload().GetBody()) != "42" {
			b.Fatalf("payload = %q, want 42", got.GetPayload().GetBody())
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
		_ = json.NewEncoder(w).Encode(neo.Response{Result: benchOutput{Sum: input.A + input.B}})
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

	for range b.N {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
		if err != nil {
			b.Fatal(err)
		}

		res, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}

		var rpcRes neo.Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			if closeErr := res.Body.Close(); closeErr != nil {
				b.Fatalf("close response body: %v", closeErr)
			}
			b.Fatal(err)
		}
		if err := res.Body.Close(); err != nil {
			b.Fatalf("close response body: %v", err)
		}

		if res.StatusCode != http.StatusOK {
			b.Fatalf("status = %d", res.StatusCode)
		}
	}
}

func BenchmarkNeoMutationHTTPServer(b *testing.B) {
	procedure := newBenchmarkNeoClient(b, neo.ProcedureKindMutation)
	ctx := context.Background()
	input := benchInput{A: 40, B: 2}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		got, err := neo.CallTyped[benchInput, benchOutput](ctx, procedure, input)
		if err != nil {
			b.Fatal(err)
		}
		if got.Sum != 42 {
			b.Fatalf("sum = %d, want 42", got.Sum)
		}
	}
}

func BenchmarkNeoMutationBufConn(b *testing.B) {
	procedure, cleanup := newBenchmarkNeoBufConnClient(b, neo.ProcedureKindMutation)
	defer cleanup()

	ctx := context.Background()
	input := benchInput{A: 40, B: 2}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		got, err := neo.CallTyped[benchInput, benchOutput](ctx, procedure, input)
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
		var req neo.Request
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
		_ = json.NewEncoder(w).Encode(neo.Response{Result: benchOutput{Sum: input.A + input.B}})
	}))
	b.Cleanup(server.Close)

	client := http.DefaultClient
	input := neo.Request{Input: benchInput{A: 40, B: 2}}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
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

		var rpcRes neo.Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			if closeErr := res.Body.Close(); closeErr != nil {
				b.Fatalf("close response body: %v", closeErr)
			}
			b.Fatal(err)
		}
		if err := res.Body.Close(); err != nil {
			b.Fatalf("close response body: %v", err)
		}

		if res.StatusCode != http.StatusOK {
			b.Fatalf("status = %d", res.StatusCode)
		}
	}
}
