package neo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

type testInput struct {
	Name string `json:"name"`
}

type testOutput struct {
	Message string `json:"message"`
}

type testNoInput struct{}

func newTestServer(router *Router) *httptest.Server {
	mux := http.NewServeMux()
	router.ServeHTTP(mux, "/neo/")
	return httptest.NewServer(mux)
}

func TestClientQueryOverHTTP(t *testing.T) {
	router := NewRouter()
	router.Register("hello", Query[testInput, testOutput](func(ctx context.Context, input testInput) (testOutput, error) {
		return testOutput{Message: "hello " + input.Name}, nil
	}))

	server := newTestServer(router)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	got, err := CallTyped[testInput, testOutput](context.Background(), client.Query.Procedure("hello"), testInput{Name: "Neo"})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if got.Message != "hello Neo" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestClientMutationOverHTTP(t *testing.T) {
	router := NewRouter()
	router.Register("user.create", Mutation[testInput, testOutput](func(ctx context.Context, input testInput) (testOutput, error) {
		return testOutput{Message: "created " + input.Name}, nil
	}))

	server := newTestServer(router)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	got, err := CallTyped[testInput, testOutput](context.Background(), client.Mutation.Procedure("user.create"), testInput{Name: "Kamil"})
	if err != nil {
		t.Fatalf("mutation failed: %v", err)
	}
	if got.Message != "created Kamil" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestMiddlewareOrder(t *testing.T) {
	var calls []string

	first := func(next Handler) Handler {
		return func(ctx context.Context, input any) (any, error) {
			calls = append(calls, "first:before")
			out, err := next(ctx, input)
			calls = append(calls, "first:after")
			return out, err
		}
	}

	second := func(next Handler) Handler {
		return func(ctx context.Context, input any) (any, error) {
			calls = append(calls, "second:before")
			out, err := next(ctx, input)
			calls = append(calls, "second:after")
			return out, err
		}
	}

	router := NewRouter()
	router.Use(first, second)
	router.Register("hello", Query[testInput, testOutput](func(ctx context.Context, input testInput) (testOutput, error) {
		calls = append(calls, "handler")
		return testOutput{Message: input.Name}, nil
	}))

	server := newTestServer(router)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	_, err := CallTyped[testInput, testOutput](context.Background(), client.Query.Procedure("hello"), testInput{Name: "Neo"})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	want := []string{"first:before", "second:before", "handler", "second:after", "first:after"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("middleware order mismatch:\n got: %#v\nwant: %#v", calls, want)
	}
}

func TestNestedRouterAndMetadata(t *testing.T) {
	root := NewRouter()
	users := NewRouter()
	users.Register("get", Query[testInput, testOutput](func(ctx context.Context, input testInput) (testOutput, error) {
		return testOutput{Message: "user " + input.Name}, nil
	}))
	root.Nested("user", users)

	server := newTestServer(root)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	got, err := CallTyped[testInput, testOutput](context.Background(), client.Query.Procedure("user.get"), testInput{Name: "42"})
	if err != nil {
		t.Fatalf("nested query failed: %v", err)
	}
	if got.Message != "user 42" {
		t.Fatalf("unexpected result: %#v", got)
	}

	metas := root.Metadata()
	if len(metas) != 1 {
		t.Fatalf("expected one metadata entry, got %d", len(metas))
	}
	if metas[0].Key != "user.get" || metas[0].Kind != ProcedureKindQuery {
		t.Fatalf("unexpected metadata: %#v", metas[0])
	}
}

func TestRouterNotFound(t *testing.T) {
	router := NewRouter()
	server := newTestServer(router)
	defer server.Close()

	res, err := http.Get(server.URL + "/neo/missing")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}

	var rpcRes Response
	if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpcRes.Error == "" {
		t.Fatalf("expected error response, got %#v", rpcRes)
	}
}

func TestSubscriptionReceivesEvent(t *testing.T) {
	router := NewRouter()
	router.RegisterSubscription("user.changes", Subscription[testNoInput, Event](func(ctx context.Context, input testNoInput) (<-chan Event, error) {
		rawEvents := router.Events().Subscribe(ctx, "users")
		out := make(chan Event)

		go func() {
			defer close(out)
			for raw := range rawEvents {
				event, ok := raw.(Event)
				if !ok {
					continue
				}

				select {
				case <-ctx.Done():
					return
				case out <- event:
				}
			}
		}()

		return out, nil
	}))

	server := newTestServer(router)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := NewClient(server.URL + "/neo")
	stream, err := SubscribeTyped[testNoInput, Event](ctx, client.Subscription.Procedure("user.changes"), testNoInput{})
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	router.Events().Publish("users", Event{Topic: "users", Name: "created", Data: "kamil"})

	select {
	case got := <-stream:
		if got.Topic != "users" || got.Name != "created" || got.Data != "kamil" {
			t.Fatalf("unexpected event: %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscription event")
	}
}
