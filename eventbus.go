package neo

import (
	"context"
	"sync"
)

// Event is a small framework-level message used by subscriptions.
type Event struct {
	Topic string `json:"topic"`
	Name  string `json:"name"`
	Data  any    `json:"data,omitempty"`
}

// EventBus is an in-memory pub/sub bus.
//
// It is intentionally tiny: good for examples, tests, and single-process apps.
// For production multi-instance deployments, replace it with Redis, NATS,
// Kafka, RabbitMQ, or Postgres LISTEN/NOTIFY behind the same Publish/Subscribe shape.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan any]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[string]map[chan any]struct{}),
	}
}

func (bus *EventBus) Publish(topic string, event any) {
	if bus == nil {
		return
	}

	bus.mu.RLock()
	defer bus.mu.RUnlock()

	for subscriber := range bus.subscribers[topic] {
		select {
		case subscriber <- event:
		default:
			// Drop instead of blocking a mutation on a slow subscription consumer.
		}
	}
}

func (bus *EventBus) Subscribe(ctx context.Context, topic string) <-chan any {
	out := make(chan any, 16)
	if bus == nil {
		close(out)
		return out
	}

	bus.mu.Lock()
	if bus.subscribers == nil {
		bus.subscribers = make(map[string]map[chan any]struct{})
	}
	if bus.subscribers[topic] == nil {
		bus.subscribers[topic] = make(map[chan any]struct{})
	}
	bus.subscribers[topic][out] = struct{}{}
	bus.mu.Unlock()

	go func() {
		<-ctx.Done()

		bus.mu.Lock()
		delete(bus.subscribers[topic], out)
		if len(bus.subscribers[topic]) == 0 {
			delete(bus.subscribers, topic)
		}
		bus.mu.Unlock()

		close(out)
	}()

	return out
}
