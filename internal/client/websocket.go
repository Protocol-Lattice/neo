package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	httptransport "github.com/Protocol-Lattice/neo/internal/transport/http"
	"github.com/Protocol-Lattice/neo/internal/transport/websocket"
)

// SubscribeWebSocketTyped opens a WebSocket subscription and decodes each
// streamed result into Out. It is useful for browsers, CLIs, and long-lived
// subscriptions where a WebSocket transport is preferable to NDJSON over HTTP.
func SubscribeWebSocketTyped[In, Out any](ctx context.Context, procedure *ClientProcedure, input In) (<-chan Out, error) {
	raw, err := procedure.SubscribeWebSocket(ctx, input)
	if err != nil {
		return nil, err
	}

	return decodeClientStream[Out](ctx, raw), nil
}

// SubscribeWebSocket opens this procedure as a WebSocket subscription.
func (procedure *ClientProcedure) SubscribeWebSocket(ctx context.Context, input any) (<-chan any, error) {
	return procedure.client.subscribeWebSocket(ctx, procedure.key, procedure.endpoint, input)
}

func (client *Client) subscribeWebSocket(ctx context.Context, key string, endpoint string, input any) (<-chan any, error) {
	ctx = ensureContext(ctx)

	httpURL, err := client.subscriptionURL(key, endpoint, input)
	if err != nil {
		return nil, err
	}
	wsURL := websocket.HTTPToURL(httpURL)

	conn, reader, err := websocket.Dial(ctx, wsURL, client.headers)
	if err != nil {
		return nil, fmt.Errorf("subscribe websocket procedure %q: %w", key, err)
	}

	out := make(chan any)
	go func() {
		defer func() {
			_ = conn.Close()
		}()
		defer close(out)

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
			payload, opcode, err := websocket.ReadFrame(reader, false)
			if err != nil {
				return
			}
			if opcode == websocket.OpcodeClose {
				return
			}
			if opcode != websocket.OpcodeText {
				continue
			}

			var rpcRes Response
			if err := json.Unmarshal(payload, &rpcRes); err != nil {
				return
			}
			if rpcRes.Error != "" || rpcRes.Code != "" {
				return
			}

			select {
			case <-ctx.Done():
				return
			case out <- rpcRes.Result:
			}
		}
	}()

	return out, nil
}

func (client *Client) subscriptionURL(key string, endpoint string, input any) (string, error) {
	if endpoint == "" {
		key = strings.Trim(key, "/")
		endpoint = client.endpoint(key)
	}
	if input == nil {
		return endpoint, nil
	}

	rawInput, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal websocket subscription input: %w", err)
	}
	return httptransport.EndpointWithInput(endpoint, rawInput), nil
}
