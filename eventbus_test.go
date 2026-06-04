package neo

import (
	"context"
	"testing"
	"time"
)

func TestEventBusPublishDeliversToSubscribers(t *testing.T) {
	bus := NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := bus.Subscribe(ctx, "users")
	bus.Publish("users", Event{Topic: "users", Name: "created", Data: "kamil"})

	select {
	case got := <-sub:
		event, ok := got.(Event)
		if !ok {
			t.Fatalf("event type = %T, want Event", got)
		}
		if event.Topic != "users" || event.Name != "created" || event.Data != "kamil" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBusPublishDoesNotDeliverAcrossTopics(t *testing.T) {
	bus := NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := bus.Subscribe(ctx, "users")
	bus.Publish("orders", Event{Topic: "orders", Name: "created"})

	select {
	case got := <-sub:
		t.Fatalf("received unexpected event: %#v", got)
	case <-time.After(50 * time.Millisecond):
		// Expected: different topic should not receive the event.
	}
}

func TestEventBusSubscribeClosesOnContextCancel(t *testing.T) {
	bus := NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())

	sub := bus.Subscribe(ctx, "users")
	cancel()

	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("subscription channel is still open after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription close")
	}
}

func TestNilEventBusSubscribeReturnsClosedChannel(t *testing.T) {
	var bus *EventBus
	sub := bus.Subscribe(context.Background(), "users")

	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("nil bus subscription should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for nil bus subscription close")
	}
}

func TestNilEventBusPublishDoesNotPanic(t *testing.T) {
	var bus *EventBus
	bus.Publish("users", Event{Name: "created"})
}
