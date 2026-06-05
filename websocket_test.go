package neo

import (
	"bufio"
	"bytes"
	"context"
	"strings"
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

func TestReadWebSocketUpgradeResponseParsesHeadersWithoutHTTPReadResponse(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: keep-alive, Upgrade\r\nSec-WebSocket-Accept: abc\r\n\r\n"))

	res, err := readWebSocketUpgradeResponse(reader)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if res.statusCode != 101 {
		t.Fatalf("status = %d, want 101", res.statusCode)
	}
	if !headerMapContains(res.header, "Upgrade", "websocket") {
		t.Fatal("missing websocket upgrade header")
	}
	if !headerMapContains(res.header, "Connection", "upgrade") {
		t.Fatal("missing connection upgrade token")
	}
	if got := headerMapGet(res.header, "Sec-WebSocket-Accept"); got != "abc" {
		t.Fatalf("accept = %q, want abc", got)
	}
}

func TestReadWebSocketUpgradeResponseRejectsMalformedHeaders(t *testing.T) {
	cases := map[string]string{
		"missing crlf":   "HTTP/1.1 101 Switching Protocols\n\r\n",
		"folded header":  "HTTP/1.1 101 Switching Protocols\r\n Upgrade: websocket\r\n\r\n",
		"malformed line": "HTTP/1.1 101 Switching Protocols\r\nUpgrade websocket\r\n\r\n",
		"bad status":     "HTTP/1.1 nope Switching Protocols\r\n\r\n",
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readWebSocketUpgradeResponse(bufio.NewReader(strings.NewReader(raw)))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestReadLimitedHTTPLineRejectsOverLimitBeforeNewline(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 32)+"\r\n"), 8)
	_, _, err := readLimitedHTTPLine(reader, 16)
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("error = %v, want line too long", err)
	}
}

func TestReadWebSocketFrameRejectsUnexpectedMaskedServerFrame(t *testing.T) {
	frame := []byte{
		0x80 | webSocketOpcodeText,
		0x80,
		0x00, 0x00, 0x00, 0x00,
	}
	_, _, err := readWebSocketFrame(bufio.NewReader(bytes.NewReader(frame)), false)
	if err == nil || !strings.Contains(err.Error(), "unexpected masked") {
		t.Fatalf("error = %v, want unexpected masked frame", err)
	}
}

func TestIsValidHTTPHeaderName(t *testing.T) {
	valid := []string{"Authorization", "X-Trace-ID", "x_custom.header"}
	for _, name := range valid {
		if !isValidHTTPHeaderName(name) {
			t.Fatalf("%q should be valid", name)
		}
	}

	invalid := []string{"", "Bad Header", "Bad:Header", "Bad\r\nHeader"}
	for _, name := range invalid {
		if isValidHTTPHeaderName(name) {
			t.Fatalf("%q should be invalid", name)
		}
	}
}
