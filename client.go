package neo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	http    *http.Client
	addr    string
	headers http.Header

	Query        *ClientNamespace
	Mutation     *ClientNamespace
	Subscription *ClientNamespace
}

type ClientNamespace struct {
	client *Client
	method string
}

type ClientProcedure struct {
	client *Client
	method string
	key    string
}

// ClientOption customizes a Neo client without making the common case noisy.
type ClientOption func(*Client)

// WithHTTPClient replaces the default HTTP client. Nil is ignored.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(client *Client) {
		if httpClient != nil {
			client.http = httpClient
		}
	}
}

// WithHeader adds a header to every request. Empty names are ignored.
func WithHeader(name, value string) ClientOption {
	return func(client *Client) {
		if name == "" {
			return
		}
		client.headers.Set(name, value)
	}
}

// WithHeaders adds all provided headers to every request.
func WithHeaders(headers http.Header) ClientOption {
	return func(client *Client) {
		for name, values := range headers {
			for _, value := range values {
				client.headers.Add(name, value)
			}
		}
	}
}

type Request struct {
	Input any `json:"input"`
}

const maxGETInputBytes = 6 << 10

type Response struct {
	Result any    `json:"result,omitempty"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

func NewClient(addr string, opts ...ClientOption) *Client {
	client := &Client{
		http:    http.DefaultClient,
		addr:    strings.TrimRight(addr, "/"),
		headers: make(http.Header),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}

	client.Query = &ClientNamespace{client: client, method: http.MethodGet}
	client.Mutation = &ClientNamespace{client: client, method: http.MethodPost}
	client.Subscription = &ClientNamespace{client: client, method: http.MethodGet}

	return client
}

func (namespace *ClientNamespace) Procedure(key string) *ClientProcedure {
	return &ClientProcedure{
		client: namespace.client,
		method: namespace.method,
		key:    strings.Trim(key, "/"),
	}
}

func (procedure *ClientProcedure) Call(ctx context.Context, input any) (any, error) {
	return procedure.client.call(ctx, procedure.method, procedure.key, input)
}

func (procedure *ClientProcedure) Subscribe(ctx context.Context, input any) (<-chan any, error) {
	return procedure.client.subscribe(ctx, procedure.key, input)
}

func CallTyped[In, Out any](ctx context.Context, procedure *ClientProcedure, input In) (Out, error) {
	var zero Out

	result, err := procedure.Call(ctx, input)
	if err != nil {
		return zero, err
	}

	return decodeClientValue[Out](result)
}

func SubscribeTyped[In, Out any](ctx context.Context, procedure *ClientProcedure, input In) (<-chan Out, error) {
	raw, err := procedure.Subscribe(ctx, input)
	if err != nil {
		return nil, err
	}

	out := make(chan Out)
	go func() {
		defer close(out)
		for value := range raw {
			decoded, err := decodeClientValue[Out](value)
			if err != nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case out <- decoded:
			}
		}
	}()

	return out, nil
}

func decodeClientValue[T any](value any) (T, error) {
	var zero T
	if typed, ok := value.(T); ok {
		return typed, nil
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return zero, fmt.Errorf("marshal typed result: %w", err)
	}

	if err := json.Unmarshal(raw, &zero); err != nil {
		return zero, fmt.Errorf("decode typed result into %s: %w", typeName[T](), err)
	}

	return zero, nil
}

func (client *Client) call(ctx context.Context, method string, key string, input any) (any, error) {
	req, err := client.newRequest(ctx, method, key, input)
	if err != nil {
		return nil, err
	}

	res, err := client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call procedure %q: %w", key, err)
	}
	defer res.Body.Close()

	var rpcRes Response
	if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if res.StatusCode >= 400 {
		if rpcRes.Error == "" && rpcRes.Code == "" {
			return nil, Errorf(CodeInternal, "procedure %q failed with status %d", key, res.StatusCode)
		}

		return nil, responseError(rpcRes)
	}

	if rpcRes.Error != "" || rpcRes.Code != "" {
		return nil, responseError(rpcRes)
	}

	return rpcRes.Result, nil
}

func (client *Client) subscribe(ctx context.Context, key string, input any) (<-chan any, error) {
	req, err := client.newRequest(ctx, http.MethodGet, key, input)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/x-ndjson")

	res, err := client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscribe procedure %q: %w", key, err)
	}

	if res.StatusCode >= 400 {
		defer res.Body.Close()

		var rpcRes Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			return nil, Errorf(CodeInternal, "subscription %q failed with status %d", key, res.StatusCode)
		}
		if rpcRes.Error != "" || rpcRes.Code != "" {
			return nil, responseError(rpcRes)
		}

		return nil, Errorf(CodeInternal, "subscription %q failed with status %d", key, res.StatusCode)
	}

	out := make(chan any)

	go func() {
		defer res.Body.Close()
		defer close(out)

		// bufio.Reader.ReadBytes grows to fit any line length, unlike
		// bufio.Scanner which silently aborts the stream on lines over
		// 64 KiB (bufio.MaxScanTokenSize).
		reader := bufio.NewReader(res.Body)
		for {
			line, err := reader.ReadBytes('\n')

			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				var rpcRes Response
				if jerr := json.Unmarshal(trimmed, &rpcRes); jerr != nil {
					return
				}
				if rpcRes.Error != "" {
					return
				}

				select {
				case <-ctx.Done():
					return
				case out <- rpcRes.Result:
				}
			}

			if err != nil {
				// io.EOF or a transport/context error: the stream is done.
				return
			}
		}
	}()

	return out, nil
}

func (client *Client) newRequest(ctx context.Context, method string, key string, input any) (*http.Request, error) {
	endpoint := fmt.Sprintf("%s/%s", client.addr, strings.Trim(key, "/"))

	var body *bytes.Reader

	switch method {
	case http.MethodGet:
		body = bytes.NewReader(nil)

		if input != nil {
			rawInput, err := json.Marshal(input)
			if err != nil {
				return nil, fmt.Errorf("marshal query input: %w", err)
			}

			if len(rawInput) > maxGETInputBytes {
				rawBody, err := json.Marshal(Request{Input: input})
				if err != nil {
					return nil, fmt.Errorf("marshal query body: %w", err)
				}
				method = http.MethodPost
				body = bytes.NewReader(rawBody)
				break
			}

			endpoint += "?input=" + url.QueryEscape(string(rawInput))
		}

	case http.MethodPost:
		rawBody, err := json.Marshal(Request{Input: input})
		if err != nil {
			return nil, fmt.Errorf("marshal mutation input: %w", err)
		}

		body = bytes.NewReader(rawBody)

	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for name, values := range client.headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	return req, nil
}
