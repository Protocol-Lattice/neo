package router_test

import (
	"context"
	"testing"
	"time"

	"github.com/Protocol-Lattice/neo/internal/broker"
)

func TestRouterUseEvents(t *testing.T) {
	custom := broker.NewEventBus()
	router := NewRouter()
	router.UseEvents(custom)

	if got := router.Events(); got != custom {
		t.Fatalf("Events() = %#v, want custom broker", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := router.Events().Subscribe(ctx, "topic")
	router.Events().Publish("topic", "ok")

	select {
	case got := <-sub:
		if got != "ok" {
			t.Fatalf("event = %#v, want ok", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestRouterUseEventsNilRestoresDefault(t *testing.T) {
	router := NewRouter()
	router.UseEvents(nil)

	if router.Events() == nil {
		t.Fatal("Events() is nil, want default in-memory broker")
	}
}
