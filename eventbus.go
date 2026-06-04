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

// EventBroker is the pub/sub boundary used by Neo subscriptions.
//
// The default implementation is EventBus, an in-memory single-process broker.
// Production applications that run more than one process should provide an
// adapter backed by Redis, NATS, Kafka, RabbitMQ, or Postgres LISTEN/NOTIFY.
//
// Publish is intentionally fire-and-forget to keep mutations fast and compatible
// with the original in-memory API. Distributed adapters should handle transient
// publish failures internally, usually by logging, metrics, retries, or a
// durable outbox owned by the application.
type EventBroker interface {
	Publish(topic string, event any)
	Subscribe(ctx context.Context, topic string) <-chan any
}

// EventBus is Neo's default in-memory EventBroker.
//
// It is intentionally tiny: good for examples, tests, and single-process apps.
// It does not distribute events across processes and does not persist messages.
// For production multi-instance deployments, use Router.UseEvents with a
// distributed EventBroker adapter.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan any]struct{}
}

var _ EventBroker = (*EventBus)(nil)

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
