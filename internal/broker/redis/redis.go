// Package redis provides a small Redis Pub/Sub implementation of neo.EventBroker.
//
// It uses only the Go standard library and the Redis RESP protocol, so using
// the adapter does not add a Redis client dependency to the root neo module.
package redis

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Protocol-Lattice/neo"
)

const (
	DefaultAddr             = "127.0.0.1:6379"
	DefaultDialTimeout      = 5 * time.Second
	DefaultHandshakeTimeout = 5 * time.Second
	DefaultSubscriberBuffer = 16
	DefaultMaxMessageBytes  = 64 << 20
)

// Logger is the small logging surface used by Broker.
type Logger interface {
	Printf(format string, args ...any)
}

// Options configures a Redis event broker.
type Options struct {
	// Addr is the Redis host:port address. Empty defaults to 127.0.0.1:6379.
	Addr string

	// Password enables AUTH before publishing or subscribing when non-empty.
	Password string

	// DB selects a Redis database before publishing or subscribing when > 0.
	DB int

	// DialTimeout bounds TCP connection establishment. Values <= 0 use the
	// default timeout.
	DialTimeout time.Duration

	// HandshakeTimeout bounds AUTH/SELECT setup. Values <= 0 use the default
	// timeout.
	HandshakeTimeout time.Duration

	// SubscriberBuffer controls how many delivered events can queue locally for a
	// Neo subscriber before the adapter starts dropping new events. Values <= 0
	// use DefaultSubscriberBuffer.
	SubscriberBuffer int

	// MaxMessageBytes bounds a single Redis pub/sub payload before allocation.
	// Values <= 0 use DefaultMaxMessageBytes.
	MaxMessageBytes int

	// Logger receives connection, publish, and subscription errors. Nil disables
	// adapter logging.
	Logger Logger
}

// Broker implements neo.EventBroker on top of Redis Pub/Sub.
//
// Publish is fire-and-forget to match neo.EventBroker. Publish failures are
// logged when Logger is set. Subscribe returns a channel immediately; if the
// Redis connection cannot be established, the channel is closed.
type Broker struct {
	addr             string
	password         string
	db               int
	dialTimeout      time.Duration
	handshakeTimeout time.Duration
	subscriberBuffer int
	maxMessageBytes  int
	logger           Logger
}

var _ neo.EventBroker = (*Broker)(nil)

// New creates a Redis-backed event broker.
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
	maxMessageBytes := opts.MaxMessageBytes
	if maxMessageBytes <= 0 {
		maxMessageBytes = DefaultMaxMessageBytes
	}

	return &Broker{
		addr:             addr,
		password:         opts.Password,
		db:               opts.DB,
		dialTimeout:      dialTimeout,
		handshakeTimeout: handshakeTimeout,
		subscriberBuffer: subscriberBuffer,
		maxMessageBytes:  maxMessageBytes,
		logger:           opts.Logger,
	}
}

// Publish serializes event as JSON and publishes it to topic.
func (broker *Broker) Publish(topic string, event any) {
	if broker == nil {
		return
	}
	if !isValidChannel(topic) {
		broker.logf("redis publish invalid channel %q", topic)
		return
	}

	payload, err := json.Marshal(event)
	if err != nil {
		broker.logf("redis publish marshal %q: %v", topic, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), broker.dialTimeout+broker.handshakeTimeout)
	defer cancel()

	conn, rw, err := broker.connect(ctx)
	if err != nil {
		broker.logf("redis publish connect %q: %v", topic, err)
		return
	}
	defer func() {
		_ = conn.Close()
	}()

	if err := writeRESPArray(rw, "PUBLISH", topic, string(payload)); err != nil {
		broker.logf("redis publish command %q: %v", topic, err)
		return
	}
	if _, err := readRESP(rw.Reader, broker.maxMessageBytes); err != nil {
		broker.logf("redis publish ack %q: %v", topic, err)
	}
}

// Subscribe subscribes to topic and returns decoded JSON events.
func (broker *Broker) Subscribe(ctx context.Context, topic string) <-chan any {
	buffer := DefaultSubscriberBuffer
	if broker != nil && broker.subscriberBuffer > 0 {
		buffer = broker.subscriberBuffer
	}
	out := make(chan any, buffer)

	if broker == nil {
		close(out)
		return out
	}
	if ctx == nil {
		broker.logf("redis subscribe %q: nil context", topic)
		close(out)
		return out
	}
	if !isValidChannel(topic) {
		broker.logf("redis subscribe invalid channel %q", topic)
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
		broker.logf("redis subscribe connect %q: %v", topic, err)
		return
	}
	defer func() {
		_ = conn.Close()
	}()

	if err := writeRESPArray(rw, "SUBSCRIBE", topic); err != nil {
		broker.logf("redis subscribe command %q: %v", topic, err)
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
		value, err := readRESP(rw.Reader, broker.maxMessageBytes)
		if err != nil {
			if ctx.Err() == nil {
				broker.logf("redis subscribe read %q: %v", topic, err)
			}
			return
		}

		values, ok := value.([]any)
		if !ok || len(values) < 3 {
			continue
		}
		kind, _ := values[0].(string)
		if strings.ToLower(kind) != "message" {
			continue
		}

		raw, ok := values[2].(string)
		if !ok {
			continue
		}
		var event any
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			broker.logf("redis subscribe decode %q: %v", topic, err)
			continue
		}

		select {
		case <-ctx.Done():
			return
		case out <- event:
		default:
			// Preserve Neo's best-effort event contract: slow subscribers drop
			// events instead of blocking the broker read loop indefinitely.
		}
	}
}

func (broker *Broker) connect(ctx context.Context) (net.Conn, *bufio.ReadWriter, error) {
	dialer := net.Dialer{Timeout: broker.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", broker.addr)
	if err != nil {
		return nil, nil, err
	}

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if broker.handshakeTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(broker.handshakeTimeout))
	}
	if broker.password != "" {
		if err := writeRESPArray(rw, "AUTH", broker.password); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
		if _, err := readRESP(rw.Reader, broker.maxMessageBytes); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
	}
	if broker.db > 0 {
		if err := writeRESPArray(rw, "SELECT", strconv.Itoa(broker.db)); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
		if _, err := readRESP(rw.Reader, broker.maxMessageBytes); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
	}
	_ = conn.SetDeadline(time.Time{})

	return conn, rw, nil
}

func writeRESPArray(rw *bufio.ReadWriter, values ...string) error {
	if _, err := fmt.Fprintf(rw, "*%d\r\n", len(values)); err != nil {
		return err
	}
	for _, value := range values {
		if _, err := fmt.Fprintf(rw, "$%d\r\n%s\r\n", len(value), value); err != nil {
			return err
		}
	}
	return rw.Flush()
}

func readRESP(reader *bufio.Reader, maxBulkBytes int) (any, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}

	switch prefix {
	case '+':
		return readLine(reader)
	case '-':
		line, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		return nil, errors.New(line)
	case ':':
		line, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		return strconv.ParseInt(line, 10, 64)
	case '$':
		line, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return "", nil
		}
		if n > maxBulkBytes {
			return nil, fmt.Errorf("bulk string length %d exceeds max %d", n, maxBulkBytes)
		}
		raw := make([]byte, n+2)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return nil, err
		}
		if raw[n] != '\r' || raw[n+1] != '\n' {
			return nil, errors.New("invalid bulk string terminator")
		}
		return string(raw[:n]), nil
	case '*':
		line, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return []any(nil), nil
		}
		values := make([]any, 0, n)
		for i := 0; i < n; i++ {
			value, err := readRESP(reader, maxBulkBytes)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unexpected RESP prefix %q", prefix)
	}
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

func isValidChannel(channel string) bool {
	return channel != "" && !strings.ContainsAny(channel, "\r\n")
}

func (broker *Broker) logf(format string, args ...any) {
	if broker != nil && broker.logger != nil {
		broker.logger.Printf(format, args...)
	}
}
