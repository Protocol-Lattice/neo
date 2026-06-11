package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	jsoncodec "github.com/Protocol-Lattice/neo/internal/codec/json"
	httptransport "github.com/Protocol-Lattice/neo/internal/transport/http"
)

type Client struct {
	http    *http.Client
	addr    string
	headers http.Header
	binary  bool

	Query        *ClientNamespace
	Mutation     *ClientNamespace
	Subscription *ClientNamespace
}

type ClientNamespace struct {
	client *Client
	method string
}

type ClientProcedure struct {
	client   *Client
	method   string
	key      string
	endpoint string
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

// WithBinaryCodec makes unary Go client calls use Neo's protobuf-like binary
// request and response envelopes. Metadata, NDJSON subscriptions, and
// WebSocket subscriptions intentionally remain JSON-based for browser and
// streaming compatibility.
func WithBinaryCodec() ClientOption {
	return func(client *Client) {
		client.binary = true
	}
}

type Request = jsoncodec.Request

const maxGETInputBytes = 6 << 10

type Response = jsoncodec.Response

type typedResponse[T any] struct {
	Result T      `json:"result,omitempty"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

type typedRequest[T any] struct {
	Input T `json:"input"`
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
	key = strings.Trim(key, "/")
	return &ClientProcedure{
		client:   namespace.client,
		method:   namespace.method,
		key:      key,
		endpoint: namespace.client.endpoint(key),
	}
}

func (procedure *ClientProcedure) Call(ctx context.Context, input any) (any, error) {
	return procedure.client.call(ctx, procedure.method, procedure.key, procedure.endpoint, input)
}

func (procedure *ClientProcedure) Subscribe(ctx context.Context, input any) (<-chan any, error) {
	return procedure.client.subscribe(ctx, procedure.key, procedure.endpoint, input)
}

// Metadata fetches procedure metadata from the server's reserved metadata
// endpoint.
func (client *Client) Metadata(ctx context.Context) ([]ProcedureMeta, error) {
	return fetchProcedureMetadata(ctx, client.http, client.endpoint(MetadataPath), client.headers)
}

func CallTyped[In, Out any](ctx context.Context, procedure *ClientProcedure, input In) (Out, error) {
	return callTyped[In, Out](ctx, procedure.client, procedure.method, procedure.key, procedure.endpoint, input)
}

func SubscribeTyped[In, Out any](ctx context.Context, procedure *ClientProcedure, input In) (<-chan Out, error) {
	return subscribeTyped[In, Out](ctx, procedure.client, procedure.key, procedure.endpoint, input)
}

func decodeClientStream[Out any](ctx context.Context, raw <-chan any) <-chan Out {
	return mapStream(ctx, raw, func(value any) (Out, bool) {
		decoded, err := decodeClientValue[Out](value)
		if err != nil {
			var zero Out
			return zero, false
		}
		return decoded, true
	})
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

func (client *Client) endpoint(key string) string {
	return client.addr + "/" + key
}

func (client *Client) call(ctx context.Context, method string, key string, endpoint string, input any) (any, error) {
	req, err := client.newProcedureRequest(ctx, method, key, endpoint, input)
	if err != nil {
		return nil, err
	}

	res, err := client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call procedure %q: %w", key, err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	var rpcRes Response
	if err := decodeHTTPResponse(res, &rpcRes); err != nil {
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

func callTyped[In, Out any](ctx context.Context, client *Client, method string, key string, endpoint string, input In) (Out, error) {
	var zero Out

	req, err := newTypedProcedureRequest(ctx, client, method, key, endpoint, input)
	if err != nil {
		return zero, err
	}

	res, err := client.http.Do(req)
	if err != nil {
		return zero, fmt.Errorf("call procedure %q: %w", key, err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	var rpcRes typedResponse[Out]
	if err := decodeHTTPResponse(res, &rpcRes); err != nil {
		return zero, fmt.Errorf("decode response: %w", err)
	}

	if res.StatusCode >= 400 {
		if rpcRes.Error == "" && rpcRes.Code == "" {
			return zero, Errorf(CodeInternal, "procedure %q failed with status %d", key, res.StatusCode)
		}

		return zero, responseError(Response{Code: rpcRes.Code, Error: rpcRes.Error})
	}

	if rpcRes.Error != "" || rpcRes.Code != "" {
		return zero, responseError(Response{Code: rpcRes.Code, Error: rpcRes.Error})
	}

	return rpcRes.Result, nil
}

func (client *Client) subscribe(ctx context.Context, key string, endpoint string, input any) (<-chan any, error) {
	ctx = ensureContext(ctx)

	req, err := client.newJSONProcedureRequest(ctx, http.MethodGet, key, endpoint, input)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", jsoncodec.NDJSONContentType)

	res, err := client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscribe procedure %q: %w", key, err)
	}

	if res.StatusCode >= 400 {
		defer func() {
			_ = res.Body.Close()
		}()

		var rpcRes Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			return nil, Errorf(CodeInternal, "subscription %q failed with status %d", key, res.StatusCode)
		}
		if rpcRes.Error != "" || rpcRes.Code != "" {
			return nil, responseError(rpcRes)
		}

		return nil, Errorf(CodeInternal, "subscription %q failed with status %d", key, res.StatusCode)
	}

	return streamTypedResponseResults[any](ctx, res.Body), nil
}

func subscribeTyped[In, Out any](ctx context.Context, client *Client, key string, endpoint string, input In) (<-chan Out, error) {
	ctx = ensureContext(ctx)

	req, err := newJSONTypedProcedureRequest(ctx, client, http.MethodGet, key, endpoint, input)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", jsoncodec.NDJSONContentType)

	res, err := client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscribe procedure %q: %w", key, err)
	}

	if res.StatusCode >= 400 {
		defer func() {
			_ = res.Body.Close()
		}()

		var rpcRes Response
		if err := json.NewDecoder(res.Body).Decode(&rpcRes); err != nil {
			return nil, Errorf(CodeInternal, "subscription %q failed with status %d", key, res.StatusCode)
		}
		if rpcRes.Error != "" || rpcRes.Code != "" {
			return nil, responseError(rpcRes)
		}

		return nil, Errorf(CodeInternal, "subscription %q failed with status %d", key, res.StatusCode)
	}

	return streamTypedResponseResults[Out](ctx, res.Body), nil
}

func streamTypedResponseResults[Out any](ctx context.Context, body io.ReadCloser) <-chan Out {
	out := make(chan Out)

	go func() {
		defer func() {
			_ = body.Close()
		}()
		defer close(out)

		// bufio.Reader.ReadBytes grows to fit any line length, unlike
		// bufio.Scanner which silently aborts the stream on lines over
		// 64 KiB (bufio.MaxScanTokenSize).
		reader := bufio.NewReader(body)
		for {
			line, err := reader.ReadBytes('\n')

			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				var rpcRes typedResponse[Out]
				if jerr := json.Unmarshal(trimmed, &rpcRes); jerr != nil {
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

			if err != nil {
				// io.EOF or a transport/context error: the stream is done.
				return
			}
		}
	}()

	return out
}

func (client *Client) newRequest(ctx context.Context, method string, key string, input any) (*http.Request, error) {
	key = strings.Trim(key, "/")
	return client.newProcedureRequest(ctx, method, key, client.endpoint(key), input)
}

func (client *Client) newProcedureRequest(ctx context.Context, method string, key string, endpoint string, input any) (*http.Request, error) {
	if client.binary {
		return client.newBinaryProcedureRequest(ctx, method, key, endpoint, Request{Input: input}, input != nil)
	}
	return client.newJSONProcedureRequest(ctx, method, key, endpoint, input)
}

func (client *Client) newJSONProcedureRequest(ctx context.Context, method string, key string, endpoint string, input any) (*http.Request, error) {
	ctx = ensureContext(ctx)
	if endpoint == "" {
		key = strings.Trim(key, "/")
		endpoint = client.endpoint(key)
	}

	var body io.Reader

	switch method {
	case http.MethodGet:
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

			endpoint = httptransport.EndpointWithInput(endpoint, rawInput)
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

	return client.newHTTPRequest(ctx, method, endpoint, body, jsoncodec.ContentType, jsoncodec.ContentType)
}

func newTypedProcedureRequest[In any](
	ctx context.Context,
	client *Client,
	method string,
	key string,
	endpoint string,
	input In,
) (*http.Request, error) {
	if client.binary {
		return client.newBinaryProcedureRequest(ctx, method, key, endpoint, typedRequest[In]{Input: input}, any(input) != nil)
	}
	return newJSONTypedProcedureRequest(ctx, client, method, key, endpoint, input)
}

func newJSONTypedProcedureRequest[In any](
	ctx context.Context,
	client *Client,
	method string,
	key string,
	endpoint string,
	input In,
) (*http.Request, error) {
	ctx = ensureContext(ctx)
	if endpoint == "" {
		key = strings.Trim(key, "/")
		endpoint = client.endpoint(key)
	}

	var body io.Reader

	switch method {
	case http.MethodGet:
		if any(input) != nil {
			rawInput, err := json.Marshal(input)
			if err != nil {
				return nil, fmt.Errorf("marshal query input: %w", err)
			}

			if len(rawInput) > maxGETInputBytes {
				rawBody, err := json.Marshal(typedRequest[In]{Input: input})
				if err != nil {
					return nil, fmt.Errorf("marshal query body: %w", err)
				}
				method = http.MethodPost
				body = bytes.NewReader(rawBody)
				break
			}

			endpoint = httptransport.EndpointWithInput(endpoint, rawInput)
		}

	case http.MethodPost:
		rawBody, err := json.Marshal(typedRequest[In]{Input: input})
		if err != nil {
			return nil, fmt.Errorf("marshal mutation input: %w", err)
		}

		body = bytes.NewReader(rawBody)

	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}

	return client.newHTTPRequest(ctx, method, endpoint, body, jsoncodec.ContentType, jsoncodec.ContentType)
}

func (client *Client) newBinaryProcedureRequest(
	ctx context.Context,
	method string,
	key string,
	endpoint string,
	envelope any,
	hasInput bool,
) (*http.Request, error) {
	ctx = ensureContext(ctx)
	if endpoint == "" {
		key = strings.Trim(key, "/")
		endpoint = client.endpoint(key)
	}

	var body io.Reader

	switch method {
	case http.MethodGet:
		if hasInput {
			rawBody, err := NeoBinaryCodec.Marshal(envelope)
			if err != nil {
				return nil, fmt.Errorf("marshal binary query body: %w", err)
			}
			method = http.MethodPost
			body = bytes.NewReader(rawBody)
		}

	case http.MethodPost:
		rawBody, err := NeoBinaryCodec.Marshal(envelope)
		if err != nil {
			return nil, fmt.Errorf("marshal binary mutation input: %w", err)
		}
		body = bytes.NewReader(rawBody)

	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}

	return client.newHTTPRequest(ctx, method, endpoint, body, BinaryContentType, BinaryContentType)
}

func (client *Client) newHTTPRequest(
	ctx context.Context,
	method string,
	endpoint string,
	body io.Reader,
	contentType string,
	accept string,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for name, values := range client.headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accept != "" && req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", accept)
	}

	return req, nil
}

func decodeHTTPResponse(res *http.Response, value any) error {
	if httptransport.IsContentType(res.Header.Get("Content-Type"), BinaryContentType) {
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			return err
		}
		return NeoBinaryCodec.Unmarshal(raw, value)
	}

	return json.NewDecoder(res.Body).Decode(value)
}
