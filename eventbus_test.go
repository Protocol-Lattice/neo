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
