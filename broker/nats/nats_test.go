package nats

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Protocol-Lattice/neo"
)

func TestBrokerPublishesAndSubscribesThroughNATS(t *testing.T) {
	server := newFakeNATSServer(t)
	defer server.close()

	broker := New(Options{
		Addr:             server.addr,
		DialTimeout:      time.Second,
		HandshakeTimeout: time.Second,
		SubscriberBuffer: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := broker.Subscribe(ctx, "feed")
	server.waitForSubscribers(t, "feed", 1)

	broker.Publish("feed", neo.Event{Topic: "feed", Name: "post.created", Data: map[string]any{"id": 1}})

	select {
	case raw, ok := <-stream:
		if !ok {
			t.Fatal("subscription closed before receiving event")
		}
		body, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		var event neo.Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if event.Topic != "feed" || event.Name != "post.created" {
			t.Fatalf("event = %#v, want feed/post.created", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBrokerSubscribeClosesOnConnectionFailure(t *testing.T) {
	broker := New(Options{
		Addr:             "127.0.0.1:1",
		DialTimeout:      10 * time.Millisecond,
		HandshakeTimeout: 10 * time.Millisecond,
	})

	stream := broker.Subscribe(context.Background(), "feed")
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not close after connection failure")
	}
}

type fakeNATSServer struct {
	ln          net.Listener
	addr        string
	mu          sync.Mutex
	subscribers map[string][]*fakeNATSSubscriber
}

type fakeNATSSubscriber struct {
	sid string
	rw  *bufio.ReadWriter
	mu  sync.Mutex
}

func newFakeNATSServer(t *testing.T) *fakeNATSServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := &fakeNATSServer{
		ln:          ln,
		addr:        ln.Addr().String(),
		subscribers: make(map[string][]*fakeNATSSubscriber),
	}

	go server.accept()
	return server
}

func (server *fakeNATSServer) close() {
	_ = server.ln.Close()
}

func (server *fakeNATSServer) accept() {
	for {
		conn, err := server.ln.Accept()
		if err != nil {
			return
		}
		go server.handle(conn)
	}
}

func (server *fakeNATSServer) handle(conn net.Conn) {
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	_, _ = rw.WriteString("INFO {\"server_id\":\"test\"}\r\n")
	_ = rw.Flush()

	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return
		}
		line = strings.TrimRight(line, "\r\n")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "CONNECT", "PONG":
			continue
		case "SUB":
			if len(fields) < 3 {
				return
			}
			server.mu.Lock()
			server.subscribers[fields[1]] = append(server.subscribers[fields[1]], &fakeNATSSubscriber{sid: fields[2], rw: rw})
			server.mu.Unlock()
		case "PUB":
			if len(fields) < 3 {
				return
			}
			n, err := strconv.Atoi(fields[2])
			if err != nil {
				return
			}
			payload := make([]byte, n)
			if _, err := io.ReadFull(rw, payload); err != nil {
				return
			}
			terminator := make([]byte, 2)
			if _, err := io.ReadFull(rw, terminator); err != nil {
				return
			}
			server.publish(fields[1], payload)
		}
	}
}

func (server *fakeNATSServer) publish(topic string, payload []byte) {
	server.mu.Lock()
	subs := append([]*fakeNATSSubscriber(nil), server.subscribers[topic]...)
	server.mu.Unlock()

	for _, sub := range subs {
		sub.mu.Lock()
		_, _ = fmt.Fprintf(sub.rw, "MSG %s %s %d\r\n", topic, sub.sid, len(payload))
		_, _ = sub.rw.Write(payload)
		_, _ = sub.rw.WriteString("\r\n")
		_ = sub.rw.Flush()
		sub.mu.Unlock()
	}
}

func (server *fakeNATSServer) waitForSubscribers(t *testing.T, topic string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		got := len(server.subscribers[topic])
		server.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d subscribers on %q", want, topic)
}
