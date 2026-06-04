// Package nats provides a small NATS-backed implementation of neo.EventBroker.
//
// It intentionally uses only the Go standard library and the NATS text
// protocol, so applications can opt into distributed pub/sub without adding a
// required dependency to the root neo module.
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
	"sync/atomic"
	"time"

	"github.com/Protocol-Lattice/neo"
)

const (
	DefaultAddr             = "127.0.0.1:4222"
	DefaultDialTimeout      = 5 * time.Second
	DefaultHandshakeTimeout = 5 * time.Second
	DefaultSubscriberBuffer = 16
)

// Logger is the small logging surface used by Broker.
type Logger interface {
	Printf(format string, args ...any)
}

// Options configures a NATS event broker.
type Options struct {
	// Addr is the NATS host:port address. Empty defaults to 127.0.0.1:4222.
	Addr string

	// DialTimeout bounds TCP connection establishment. Values <= 0 use the
	// default timeout.
	DialTimeout time.Duration

	// HandshakeTimeout bounds the initial NATS INFO/CONNECT exchange. Values <= 0
	// use the default timeout.
	HandshakeTimeout time.Duration

	// SubscriberBuffer controls how many delivered events can queue locally for a
	// Neo subscriber before the adapter starts dropping new events for that
	// subscriber. Values <= 0 use DefaultSubscriberBuffer.
	SubscriberBuffer int

	// Logger receives connection, publish, and subscription errors. Nil disables
	// adapter logging.
	Logger Logger
}

// Broker implements neo.EventBroker on top of NATS Pub/Sub.
//
// Publish is fire-and-forget to match neo.EventBroker. Publish failures are
// logged when Logger is set. Subscribe returns a channel immediately; if the
// NATS connection cannot be established, the channel is closed.
type Broker struct {
	addr             string
	dialTimeout      time.Duration
	handshakeTimeout time.Duration
	subscriberBuffer int
	logger           Logger
	nextSID          atomic.Uint64
}

var _ neo.EventBroker = (*Broker)(nil)

// New creates a NATS-backed event broker.
func New(opts Options) *Broker {
	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	handshakeTimeout := opts.HandshakeTimeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = DefaultHandshakeTimeout
	}
	subscriberBuffer := opts.SubscriberBuffer
	if subscriberBuffer <= 0 {
		subscriberBuffer = DefaultSubscriberBuffer
	}

	return &Broker{
		addr:             addr,
		dialTimeout:      dialTimeout,
		handshakeTimeout: handshakeTimeout,
		subscriberBuffer: subscriberBuffer,
		logger:           opts.Logger,
	}
}

// Publish serializes event as JSON and publishes it to topic.
func (broker *Broker) Publish(topic string, event any) {
	if broker == nil || strings.TrimSpace(topic) == "" {
		return
	}

	payload, err := json.Marshal(event)
	if err != nil {
		broker.logf("nats publish marshal %q: %v", topic, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), broker.dialTimeout+broker.handshakeTimeout)
	defer cancel()

	conn, rw, err := broker.connect(ctx)
	if err != nil {
		broker.logf("nats publish connect %q: %v", topic, err)
		return
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(rw, "PUB %s %d\r\n", topic, len(payload)); err != nil {
		broker.logf("nats publish command %q: %v", topic, err)
		return
	}
	if _, err := rw.Write(payload); err != nil {
		broker.logf("nats publish payload %q: %v", topic, err)
		return
	}
	if _, err := rw.WriteString("\r\n"); err != nil {
		broker.logf("nats publish terminator %q: %v", topic, err)
		return
	}
	if err := rw.Flush(); err != nil {
		broker.logf("nats publish flush %q: %v", topic, err)
	}
}

// Subscribe subscribes to topic and returns decoded JSON events.
func (broker *Broker) Subscribe(ctx context.Context, topic string) <-chan any {
	buffer := DefaultSubscriberBuffer
	if broker != nil && broker.subscriberBuffer > 0 {
		buffer = broker.subscriberBuffer
	}
	out := make(chan any, buffer)

	if broker == nil || strings.TrimSpace(topic) == "" {
		close(out)
		return out
	}

	go broker.subscribe(ctx, topic, out)
	return out
}

func (broker *Broker) subscribe(ctx context.Context, topic string, out chan any) {
	defer close(out)

	conn, rw, err := broker.connect(ctx)
	if err != nil {
		broker.logf("nats subscribe connect %q: %v", topic, err)
		return
	}
	defer conn.Close()

	sid := broker.nextSID.Add(1)
	if _, err := fmt.Fprintf(rw, "SUB %s %d\r\n", topic, sid); err != nil {
		broker.logf("nats subscribe command %q: %v", topic, err)
		return
	}
	if err := rw.Flush(); err != nil {
		broker.logf("nats subscribe flush %q: %v", topic, err)
		return
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			if ctx.Err() == nil && err != io.EOF {
				broker.logf("nats subscribe read %q: %v", topic, err)
			}
			return
		}

		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "" || line == "+OK" || strings.HasPrefix(line, "INFO "):
			continue
		case strings.HasPrefix(line, "-ERR"):
			broker.logf("nats subscribe server error %q: %s", topic, line)
			return
		case line == "PING":
			if _, err := rw.WriteString("PONG\r\n"); err != nil {
				broker.logf("nats subscribe pong %q: %v", topic, err)
				return
			}
			if err := rw.Flush(); err != nil {
				broker.logf("nats subscribe pong flush %q: %v", topic, err)
				return
			}
		case strings.HasPrefix(line, "MSG "):
			msg, err := readMSG(rw, line)
			if err != nil {
				broker.logf("nats subscribe msg %q: %v", topic, err)
				return
			}

			var event any
			if err := json.Unmarshal(msg, &event); err != nil {
				broker.logf("nats subscribe decode %q: %v", topic, err)
				continue
			}

			select {
			case <-ctx.Done():
				return
			case out <- event:
			default:
				// Preserve Neo's non-blocking event contract: slow subscribers lose
				// events instead of blocking the broker read loop forever.
			}
		default:
			broker.logf("nats subscribe unknown line %q: %s", topic, line)
		}
	}
}

func (broker *Broker) connect(ctx context.Context) (net.Conn, *bufio.ReadWriter, error) {
	dialer := net.Dialer{Timeout: broker.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", broker.addr)
	if err != nil {
		return nil, nil, err
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(broker.handshakeTimeout))
	}

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	line, err := rw.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("read INFO: %w", err)
	}
	if !strings.HasPrefix(line, "INFO ") {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("expected INFO, got %q", strings.TrimSpace(line))
	}

	if _, err := rw.WriteString("CONNECT {\"verbose\":false,\"pedantic\":false,\"lang\":\"go\",\"version\":\"neo\"}\r\n"); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	_ = conn.SetDeadline(time.Time{})
	return conn, rw, nil
}

func readMSG(rw *bufio.ReadWriter, header string) ([]byte, error) {
	fields := strings.Fields(header)
	if len(fields) != 4 && len(fields) != 5 {
		return nil, fmt.Errorf("invalid MSG header %q", header)
	}

	bytesField := fields[len(fields)-1]
	n, err := strconv.Atoi(bytesField)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("invalid MSG size %q", bytesField)
	}

	payload := make([]byte, n)
	if _, err := io.ReadFull(rw, payload); err != nil {
		return nil, err
	}

	terminator := make([]byte, 2)
	if _, err := io.ReadFull(rw, terminator); err != nil {
		return nil, err
	}
	if string(terminator) != "\r\n" {
		return nil, fmt.Errorf("invalid MSG terminator %q", string(terminator))
	}

	return payload, nil
}

func (broker *Broker) logf(format string, args ...any) {
	if broker != nil && broker.logger != nil {
		broker.logger.Printf(format, args...)
	}
}
