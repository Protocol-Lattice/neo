package neo

import (
	"context"
	"testing"
	"time"
)

func TestWebSocketSubscriptionStreamsValues(t *testing.T) {
	router := NewRouter()
	router.RegisterSubscription("events", Subscription[testInput, testOutput](func(ctx context.Context, in testInput) (<-chan testOutput, error) {
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
	router := NewRouter()
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
