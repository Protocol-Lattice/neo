package redis

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/Protocol-Lattice/neo"
)

func TestPublishWritesRedisPublishCommand(t *testing.T) {
	commands := make(chan []any, 1)
	server := newFakeRedisServer(t, func(conn net.Conn) {
		defer func() {
			_ = conn.Close()
		}()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		cmd, err := readRESP(rw.Reader, DefaultMaxMessageBytes)
		if err != nil {
			t.Errorf("read command: %v", err)
			return
		}
		values, ok := cmd.([]any)
		if !ok {
			t.Errorf("command type = %T, want []any", cmd)
			return
		}
		commands <- values
		_, _ = rw.WriteString(":1\r\n")
		_ = rw.Flush()
	})
	defer server.close()

	broker := New(Options{Addr: server.addr})
	broker.Publish("feed", neo.Event{Topic: "feed", Name: "post.created", Data: map[string]any{"id": 1}})

	select {
	case cmd := <-commands:
		if got, want := commandString(cmd, 0), "PUBLISH"; got != want {
			t.Fatalf("command = %q, want %q", got, want)
		}
		if got, want := commandString(cmd, 1), "feed"; got != want {
			t.Fatalf("channel = %q, want %q", got, want)
		}
		var event neo.Event
		if err := json.Unmarshal([]byte(commandString(cmd, 2)), &event); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if event.Name != "post.created" {
			t.Fatalf("event name = %q, want post.created", event.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for publish command")
	}
}

func TestSubscribeReadsRedisMessages(t *testing.T) {
	server := newFakeRedisServer(t, func(conn net.Conn) {
		defer func() {
			_ = conn.Close()
		}()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		cmd, err := readRESP(rw.Reader, DefaultMaxMessageBytes)
		if err != nil {
			t.Errorf("read command: %v", err)
			return
		}
		values, ok := cmd.([]any)
		if !ok {
			t.Errorf("command type = %T, want []any", cmd)
			return
		}
		if got, want := commandString(values, 0), "SUBSCRIBE"; got != want {
			t.Errorf("command = %q, want %q", got, want)
			return
		}

		_ = writeRESPArray(rw, "subscribe", "feed", "1")
		payload, _ := json.Marshal(neo.Event{Topic: "feed", Name: "post.created", Data: map[string]any{"id": 1}})
		_ = writeRESPArray(rw, "message", "feed", string(payload))
	})
	defer server.close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	broker := New(Options{Addr: server.addr})
	stream := broker.Subscribe(ctx, "feed")

	select {
	case raw, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before message")
		}
		event, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("event type = %T, want map[string]any", raw)
		}
		if event["name"] != "post.created" {
			t.Fatalf("event = %#v, want post.created", event)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for redis message")
	}
}

func TestSubscribeRejectsInvalidChannel(t *testing.T) {
	broker := New(Options{})
	stream := broker.Subscribe(context.Background(), "feed\r\nPING")

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("stream open, want closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invalid channel stream to close")
	}
}

func TestRESPRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer func() {
		_ = client.Close()
		_ = server.Close()
	}()

	go func() {
		rw := bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server))
		_ = writeRESPArray(rw, "PUBLISH", "feed", `{"ok":true}`)
	}()

	reader := bufio.NewReader(client)
	got, err := readRESP(reader, DefaultMaxMessageBytes)
	if err != nil {
		t.Fatalf("read RESP: %v", err)
	}
	want := []any{"PUBLISH", "feed", `{"ok":true}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RESP = %#v, want %#v", got, want)
	}
}

type fakeRedisServer struct {
	addr  string
	close func()
}

func newFakeRedisServer(t *testing.T, handle func(net.Conn)) fakeRedisServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		handle(conn)
	}()

	return fakeRedisServer{
		addr: listener.Addr().String(),
		close: func() {
			_ = listener.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for fake redis server")
			}
		},
	}
}

func commandString(values []any, index int) string {
	if index >= len(values) {
		return ""
	}
	value, _ := values[index].(string)
	return value
}
