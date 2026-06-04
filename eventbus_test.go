package neo

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestEventBusConcurrentPublishSubscribeRace(t *testing.T) {
	bus := NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const subscribers = 32
	const publishers = 32
	const perPublisher = 100

	var wg sync.WaitGroup
	for i := 0; i < subscribers; i++ {
		ch := bus.Subscribe(ctx, "topic")
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.After(500 * time.Millisecond)
			for {
				select {
				case <-deadline:
					return
				case _, ok := <-ch:
					if !ok {
						return
					}
				}
			}
		}()
	}

	for p := 0; p < publishers; p++ {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				bus.Publish("topic", fmt.Sprintf("%d/%d", p, i))
			}
		}()
	}

	wg.Wait()
	cancel()
}

func TestRouterUseEvents(t *testing.T) {
	custom := NewEventBus()
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

func TestEventBusWithOptionsControlsSubscriberBuffer(t *testing.T) {
	bus := NewEventBusWithOptions(EventBusOptions{SubscriberBuffer: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := bus.Subscribe(ctx, "topic")
	bus.Publish("topic", "first")
	bus.Publish("topic", "second") // dropped because the subscriber buffer is full.

	select {
	case got := <-sub:
		if got != "first" {
			t.Fatalf("event = %#v, want first", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first event")
	}

	select {
	case got := <-sub:
		t.Fatalf("unexpected second event: %#v", got)
	default:
	}
}
