package neo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayMountRoutesLocalServiceWithDotPrefix(t *testing.T) {
	users := NewRouter()
	users.Register("getByID", Query(func(_ context.Context, input testInput) (testOutput, error) {
		return testOutput{ID: input.ID, Name: "Kamil"}, nil
	}))

	gateway := NewGateway()
	if err := gateway.Mount("users", users); err != nil {
		t.Fatalf("mount users: %v", err)
	}

	server := newGatewayTestServer(gateway)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	got, err := CallTyped[testInput, testOutput](
		context.Background(),
		client.Query.Procedure("users.getByID"),
		testInput{ID: 1},
	)
	if err != nil {
		t.Fatalf("call users.getByID: %v", err)
	}
	if got.ID != 1 || got.Name != "Kamil" {
		t.Fatalf("result = %#v, want user", got)
	}
}

func TestGatewayMountRoutesLocalServiceWithSlashPrefix(t *testing.T) {
	users := NewRouter()
	users.Register("getByID", Query(func(_ context.Context, input testInput) (testOutput, error) {
		return testOutput{ID: input.ID, Name: "Kamil"}, nil
	}))

	gateway := NewGateway()
	if err := gateway.Mount("users", users); err != nil {
		t.Fatalf("mount users: %v", err)
	}

	mux := http.NewServeMux()
	gateway.ServeHTTP(mux, "/neo/")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, `/neo/users/getByID?input={"id":2}`, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var res typedResponse[testOutput]
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Result.ID != 2 || res.Result.Name != "Kamil" {
		t.Fatalf("result = %#v, want user", res.Result)
	}
}

func TestGatewayProxyRoutesRemoteService(t *testing.T) {
	users := NewRouter()
	users.Register("getByID", Query(func(_ context.Context, input testInput) (testOutput, error) {
		return testOutput{ID: input.ID, Name: "Trinity"}, nil
	}))

	upstreamMux := http.NewServeMux()
	users.ServeHTTP(upstreamMux, "/neo/")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Token") != "token-1" {
			writeProcedureError(w, NewError(CodeUnauthorized, "missing service token"))
			return
		}
		upstreamMux.ServeHTTP(w, r)
	}))
	defer upstream.Close()

	gateway := NewGateway()
	if err := gateway.Proxy("users", upstream.URL+"/neo", WithProxyHeader("X-Service-Token", "token-1")); err != nil {
		t.Fatalf("proxy users: %v", err)
	}

	server := newGatewayTestServer(gateway)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	got, err := CallTyped[testInput, testOutput](
		context.Background(),
		client.Query.Procedure("users.getByID"),
		testInput{ID: 7},
	)
	if err != nil {
		t.Fatalf("call proxied users.getByID: %v", err)
	}
	if got.ID != 7 || got.Name != "Trinity" {
		t.Fatalf("result = %#v, want proxied user", got)
	}
}

func TestGatewayProxyStreamsRemoteSubscription(t *testing.T) {
	events := make(chan testOutput, 1)

	users := NewRouter()
	users.RegisterSubscription("changes", Subscription(func(_ context.Context, _ struct{}) (<-chan testOutput, error) {
		return events, nil
	}))

	upstream := newTestServer(users)
	defer upstream.Close()

	gateway := NewGateway()
	if err := gateway.Proxy("users", upstream.URL+"/neo"); err != nil {
		t.Fatalf("proxy users: %v", err)
	}

	server := newGatewayTestServer(gateway)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := NewClient(server.URL + "/neo")
	stream, err := SubscribeTyped[struct{}, testOutput](
		ctx,
		client.Subscription.Procedure("users.changes"),
		struct{}{},
	)
	if err != nil {
		t.Fatalf("subscribe proxied users.changes: %v", err)
	}

	events <- testOutput{Message: "created"}

	select {
	case got, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before event")
		}
		if got.Message != "created" {
			t.Fatalf("event = %#v, want created", got)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for proxied event")
	}
}

func TestGatewayMetadataPrefixesLocalAndRemoteServices(t *testing.T) {
	users := NewRouter()
	users.Register("getByID", Query(func(context.Context, testInput) (testOutput, error) {
		return testOutput{}, nil
	}))

	gateway := NewGateway()
	if err := gateway.Mount("users", users); err != nil {
		t.Fatalf("mount users: %v", err)
	}
	if err := gateway.Proxy("orders", "http://orders.example/neo", WithProxyMetadata(ProcedureMeta{
		Key:    "create",
		Kind:   ProcedureKindMutation,
		Input:  "CreateOrderInput",
		Output: "Order",
	})); err != nil {
		t.Fatalf("proxy orders: %v", err)
	}

	metas := gateway.Metadata()
	if len(metas) != 2 {
		t.Fatalf("metadata len = %d, want 2", len(metas))
	}

	got := []string{metas[0].Key, metas[1].Key}
	want := []string{"orders.create", "users.getByID"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("metadata keys = %#v, want %#v", got, want)
	}
}

func TestGatewayUnknownServiceReturnsNotFound(t *testing.T) {
	gateway := NewGateway()
	server := newGatewayTestServer(gateway)
	defer server.Close()

	res, err := http.Get(server.URL + "/neo/users.getByID")
	if err != nil {
		t.Fatalf("get unknown service: %v", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}

	var rpcRes Response
	if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpcRes.Code != string(CodeNotFound) || rpcRes.Error != "service not found" {
		t.Fatalf("response = %#v, want service not found", rpcRes)
	}
}

func newGatewayTestServer(gateway *Gateway) *httptest.Server {
	mux := http.NewServeMux()
	gateway.ServeHTTP(mux, "/neo/")
	return httptest.NewServer(mux)
}
