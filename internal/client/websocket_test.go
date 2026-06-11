package client

import (
	"context"
	"testing"
	"time"

	routerruntime "github.com/Protocol-Lattice/neo/internal/router"
)

func TestWebSocketSubscriptionStreamsValues(t *testing.T) {
	router := routerruntime.NewRouter()
	router.RegisterSubscription("events", routerruntime.Subscription[testInput, testOutput](func(ctx context.Context, in testInput) (<-chan testOutput, error) {
		out := make(chan testOutput, 2)
		out <- testOutput{Message: "hello " + in.Name}
		out <- testOutput{Message: "bye " + in.Name}
		close(out)
		return out, nil
	}))

	server := newTestServer(router)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream, err := SubscribeWebSocketTyped[testInput, testOutput](ctx, client.Subscription.Procedure("events"), testInput{Name: "Neo"})
	if err != nil {
		t.Fatalf("subscribe websocket: %v", err)
	}

	got := []string{}
	for event := range stream {
		got = append(got, event.Message)
	}

	want := []string{"hello Neo", "bye Neo"}
	if len(got) != len(want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %#v, want %#v", got, want)
		}
	}
}

func TestWebSocketSubscriptionMissingProcedureReturnsError(t *testing.T) {
	router := routerruntime.NewRouter()
	server := newTestServer(router)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.Subscription.Procedure("missing").SubscribeWebSocket(ctx, nil)
	if err == nil {
		t.Fatal("expected missing websocket subscription error")
	}
}

func TestWebSocketSubscriptionClosesOnContextCancel(t *testing.T) {
	router := routerruntime.NewRouter()
	router.RegisterSubscription("events", routerruntime.Subscription[struct{}, testOutput](func(ctx context.Context, in struct{}) (<-chan testOutput, error) {
		return make(chan testOutput), nil
	}))

	server := newTestServer(router)
	defer server.Close()

	client := NewClient(server.URL + "/neo")
	ctx, cancel := context.WithCancel(context.Background())

	stream, err := client.Subscription.Procedure("events").SubscribeWebSocket(ctx, nil)
	if err != nil {
		t.Fatalf("subscribe websocket: %v", err)
	}

	cancel()

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("stream is open, want closed after context cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket stream to close")
	}
}
