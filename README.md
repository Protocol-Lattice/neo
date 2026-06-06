<img width="1024" height="1024" alt="ChatGPT Image 5 cze 2026 o 14_14_48" src="https://github.com/user-attachments/assets/8f0fce3d-d34b-4771-afc1-7d0ea4c5d533" />
> A lightweight, type-friendly, tRPC-style RPC framework for Go.

Neo gives Go backends a clean procedure-based API with **queries**, **mutations**, **subscriptions**, middleware, nested routers, generated metadata, and a small client API.

It is designed for developers who like the tRPC mental model, but want something idiomatic and simple in Go.

```go
router := neo.NewRouter()

router.Register("user.get", neo.Query(func(ctx context.Context, in GetUserInput) (User, error) {
	return User{ID: in.ID, Name: "Kamil"}, nil
}))

router.Register("user.create", neo.Mutation(func(ctx context.Context, in CreateUserInput) (User, error) {
	return User{ID: 1, Name: in.Name}, nil
}))
```

---

## Features

- **Query / Mutation / Subscription procedures**
- **Typed Go handlers**
- **tRPC-style client**
- **Nested routers**
- **Router merge**
- **Microservice gateway for local and remote service routers**
- **Gateway health and metadata diagnostics**
- **Middleware**
- **Event-triggered subscriptions**
- **NDJSON and WebSocket subscription transports**
- **Pluggable event broker**
- **Standard-library NATS broker adapter**
- **Standard-library Redis broker adapter**
- **CORS preflight support**
- **Safe internal error redaction**
- **POST queries for large payloads**
- **Server hardening options**
- **Codegen-friendly metadata**
- **HTTP metadata introspection endpoint**
- **Remote metadata input for code generation**
- **Generated Markdown docs and JSON schema exports**
- **Input validation hooks**
- **Race-tested in-memory event bus**

---

## Installation

```bash
go get github.com/Protocol-Lattice/neo
```

---

## Quick Start

### Server

```go
package main

import (
	"context"
	"log"

	"github.com/Protocol-Lattice/neo"
)

type HelloInput struct {
	Name string `json:"name"`
}

type HelloOutput struct {
	Message string `json:"message"`
}

func main() {
	router := neo.NewRouter()

	router.Register("hello", neo.Query(func(ctx context.Context, in HelloInput) (HelloOutput, error) {
		return HelloOutput{Message: "Hello, " + in.Name}, nil
	}))

	log.Println("Neo listening on :8080")

	if err := router.ListenAndServe(neo.ServerOptions{
		Addr:   ":8080",
		Prefix: "/neo/",
	}); err != nil {
		log.Fatal(err)
	}
}
```

Call it:

```bash
curl 'http://localhost:8080/neo/hello?input={"name":"Neo"}'
```

Response:

```json
{
  "result": {
    "message": "Hello, Neo"
  }
}
```

---

## Client

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Protocol-Lattice/neo"
)

type HelloInput struct {
	Name string `json:"name"`
}

type HelloOutput struct {
	Message string `json:"message"`
}

func main() {
	client := neo.NewClient(
	"http://localhost:8080/neo",
	neo.WithHeader("Authorization", "Bearer <token>"),
)

	out, err := neo.CallTyped[HelloInput, HelloOutput](
		context.Background(),
		client.Query.Procedure("hello"),
		HelloInput{Name: "Kamil"},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(out.Message)
}
```

---

## Microservices

Use `neo.NewGateway()` when several Neo services should share one public API.
Mount local routers for tests or development, and proxy remote services in
production.

```go
gateway := neo.NewGateway()

if err := gateway.Proxy("users", "http://localhost:8081/neo"); err != nil {
	log.Fatal(err)
}
if err := gateway.Proxy("orders", "http://localhost:8082/neo"); err != nil {
	log.Fatal(err)
}

log.Fatal(gateway.ListenAndServe(neo.ServerOptions{
	Addr:   ":8080",
	Prefix: "/neo/",
}))
```

Clients call service-prefixed procedure keys:

```go
user, err := neo.CallTyped[GetUserInput, User](
	ctx,
	client.Query.Procedure("users.getByID"),
	GetUserInput{ID: 1},
)
```

The gateway forwards `users.getByID` to `getByID` on the users service. See
`examples/microservices` for separate users, orders, gateway, and client
commands.

Gateway-only packages can still use `neo-gen`: add `neo.WithProxyMetadata(...)`
to proxied services, then run the generator against the gateway package to
produce service-prefixed typed clients.

Gateways can also discover proxy metadata from upstream Neo services:

```go
if err := gateway.Proxy(
	"users",
	"http://localhost:8081/neo",
	neo.WithProxyHeader("X-Service-Token", "internal-token"),
	neo.WithProxyMetadataDiscovery(),
); err != nil {
	log.Fatal(err)
}
```

`WithProxyMetadataDiscovery` fetches `/_meta` from the upstream service when the
proxy is configured. Headers set with `WithProxyHeader` or `WithProxyHeaders`
are sent with that metadata request.

Gateway diagnostics are available at:

```http
GET /neo/_health
```

The response lists configured services, whether each one is local or proxied,
and the metadata currently known for that service.

---

## Queries

Queries are read-style procedures.

By default, simple query inputs can be sent through `GET` query params.

```go
router.Register("user.get", neo.Query(func(ctx context.Context, in GetUserInput) (User, error) {
	return db.GetUser(ctx, in.ID)
}))
```

Example request:

```bash
curl 'http://localhost:8080/neo/user.get?input={"id":1}'
```

Neo also supports query-over-`POST` for larger inputs, avoiding URL size limits.

```bash
curl -X POST 'http://localhost:8080/neo/user.get' \
  -H 'Content-Type: application/json' \
  -d '{"input":{"id":1}}'
```

---

## Mutations

Mutations are write-style procedures and are served over `POST`.

```go
type CreateUserInput struct {
	Name string `json:"name"`
}

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

router.Register("user.create", neo.Mutation(func(ctx context.Context, in CreateUserInput) (User, error) {
	return db.CreateUser(ctx, in.Name)
}))
```

Example request:

```bash
curl -X POST 'http://localhost:8080/neo/user.create' \
  -H 'Content-Type: application/json' \
  -d '{"input":{"name":"Kamil"}}'
```

---

## Subscriptions

Subscriptions expose server-pushed events from typed Go handlers. Neo supports two subscription transports:

- **NDJSON over HTTP** — simple, curl-friendly, and the default client subscription transport.
- **WebSocket** — browser-friendly bidirectional transport for real-time applications.

```go
router.RegisterSubscription("events.feed", neo.Subscription(func(ctx context.Context, in struct{}) (<-chan any, error) {
	return router.Events().Subscribe(ctx, "feed"), nil
}))
```

A mutation can publish an event:

```go
router.Register("post.create", neo.Mutation(func(ctx context.Context, in CreatePostInput) (Post, error) {
	post := Post{ID: 1, Title: in.Title}

	router.Events().Publish("feed", neo.Event{
		Topic: "feed",
		Name:  "post.created",
		Data:  post,
	})

	return post, nil
}))
```

NDJSON client usage:

```go
stream, err := client.Subscription.Procedure("events.feed").Subscribe(ctx, nil)
if err != nil {
	log.Fatal(err)
}

for event := range stream {
	fmt.Printf("event: %#v\n", event)
}
```

WebSocket client usage:

```go
stream, err := client.Subscription.Procedure("events.feed").SubscribeWebSocket(ctx, nil)
if err != nil {
	log.Fatal(err)
}

for event := range stream {
	fmt.Printf("event: %#v\n", event)
}
```

Typed WebSocket subscriptions:

```go
type FeedEvent struct {
	Name string `json:"name"`
	Data Post   `json:"data"`
}

stream, err := neo.SubscribeWebSocketTyped[struct{}, FeedEvent](
	ctx,
	client.Subscription.Procedure("events.feed"),
	struct{}{},
)
if err != nil {
	log.Fatal(err)
}

for event := range stream {
	fmt.Println(event.Name)
}
```

---

## Event Broker

Neo ships with an in-memory event bus by default.

That is great for:

- examples
- tests
- local apps
- single-process deployments

For production multi-instance systems, use a distributed broker such as:

- Redis Pub/Sub
- NATS
- Kafka
- RabbitMQ
- Postgres `LISTEN/NOTIFY`

Neo exposes a small broker interface:

```go
type EventBroker interface {
	Publish(topic string, event any)
	Subscribe(ctx context.Context, topic string) <-chan any
}
```

Use a custom broker:

```go
router := neo.NewRouter()
router.UseEvents(myRedisBroker)
```

Use the built-in NATS adapter for multi-process pub/sub:

```go
package main

import (
	"log"

	"github.com/Protocol-Lattice/neo"
	natsbroker "github.com/Protocol-Lattice/neo/broker/nats"
)

func main() {
	router := neo.NewRouter()

	broker := natsbroker.New(natsbroker.Options{
		Addr:             "127.0.0.1:4222",
		SubscriberBuffer: 64,
		Logger:           log.Default(),
	})
	router.UseEvents(broker)

	// Register subscriptions/mutations as usual. Any Neo process using the same
	// NATS subject can publish and receive events across instances.
}
```

The NATS adapter publishes events as JSON and decodes subscription messages back into dynamic Go values. It preserves Neo's fire-and-forget event contract: publish errors are logged through `Options.Logger`, and slow local subscribers drop events when their buffer is full. For durable delivery, retries, or outbox semantics, wrap `EventBroker` with application-specific persistence.

Use the built-in Redis adapter when Redis Pub/Sub is already part of your stack:

```go
package main

import (
	"log"

	"github.com/Protocol-Lattice/neo"
	redisbroker "github.com/Protocol-Lattice/neo/broker/redis"
)

func main() {
	router := neo.NewRouter()

	broker := redisbroker.New(redisbroker.Options{
		Addr:             "127.0.0.1:6379",
		SubscriberBuffer: 64,
		Logger:           log.Default(),
	})
	router.UseEvents(broker)
}
```

The Redis adapter also publishes events as JSON and preserves Neo's best-effort
contract. Publish or subscription errors are logged through `Options.Logger`,
and slow local subscribers drop events when their buffer is full.

The default in-memory bus is intentionally tiny and non-blocking. Slow subscribers do not block mutation handlers. Delivery is best-effort and lossy: if a subscriber buffer is full, new events for that subscriber are dropped. Use a distributed broker with explicit delivery guarantees for production multi-instance systems.

---

## Middleware

Middleware wraps procedure execution.

```go
func LoggingMiddleware(next neo.Handler) neo.Handler {
	return func(ctx context.Context, input any) (any, error) {
		log.Printf("input: %#v", input)

		out, err := next(ctx, input)

		log.Printf("output: %#v err=%v", out, err)

		return out, err
	}
}
```

Use it before registering procedures:

```go
router := neo.NewRouter()

router.Use(LoggingMiddleware)

router.Register("hello", neo.Query(func(ctx context.Context, in HelloInput) (HelloOutput, error) {
	return HelloOutput{Message: "hello " + in.Name}, nil
}))
```

Middleware is snapshotted at registration time. This makes behavior predictable and avoids per-request router mutation.

---

## Nested Routers

```go
api := neo.NewRouter()
users := neo.NewRouter()

users.Register("get", neo.Query(func(ctx context.Context, in GetUserInput) (User, error) {
	return User{ID: in.ID}, nil
}))

users.Register("create", neo.Mutation(func(ctx context.Context, in CreateUserInput) (User, error) {
	return User{ID: 1, Name: in.Name}, nil
}))

api.Nested("user", users)
```

Registered procedures:

```txt
user.get
user.create
```

---

## Merge Routers

```go
app := neo.NewRouter()

auth := neo.NewRouter()
billing := neo.NewRouter()

app.Merge(auth)
app.Merge(billing)
```

This is useful for composing larger APIs from smaller modules.

---

## Error Handling

Neo has structured, machine-readable errors.

```go
return User{}, neo.NewError(neo.CodeNotFound, "user not found")
```

Response:

```json
{
  "code": "NOT_FOUND",
  "error": "user not found"
}
```

Supported error codes:

```go
neo.CodeBadRequest
neo.CodeUnauthorized
neo.CodeForbidden
neo.CodeNotFound
neo.CodeMethodNotAllowed
neo.CodeConflict
neo.CodeTooManyRequests
neo.CodeInternal
neo.CodeNotImplemented
neo.CodeUnavailable
neo.CodeTimeout
```

Plain Go errors are treated as internal server errors:

```go
return User{}, errors.New("pq: connection refused to 10.0.0.5")
```

Neo logs the full error server-side but sends a safe message to the client:

```json
{
  "code": "INTERNAL",
  "error": "internal server error"
}
```

This prevents accidental leakage of database errors, internal hosts, tokens, filesystem paths, or stack details.

---

## Server Options

Neo can be used directly as an HTTP handler or served with hardened defaults.

```go
err := router.ListenAndServe(neo.ServerOptions{
	Addr:         ":8080",
	Prefix:       "/neo/",
	ReadTimeout:  5 * time.Second,
	WriteTimeout: 10 * time.Second,
	IdleTimeout:  60 * time.Second,
	MaxRequestBody: 1 << 20,
})
if err != nil {
	log.Fatal(err)
}
```

You can also mount Neo into your own mux:

```go
mux := http.NewServeMux()

router.ServeHTTP(mux, "/neo/")

server := &http.Server{
	Addr:         ":8080",
	Handler:      mux,
	ReadTimeout:  5 * time.Second,
	WriteTimeout: 10 * time.Second,
	IdleTimeout:  60 * time.Second,
}

log.Fatal(server.ListenAndServe())
```

---

## CORS

Neo supports browser preflight requests.

`OPTIONS` requests return `204 No Content` with CORS headers. By default, Neo reflects the request `Origin` for local development. In production, configure allowed origins explicitly:

```go
router.UseCORS(neo.CORSOptions{
	AllowedOrigins:   []string{"https://app.example.com"},
	AllowedHeaders:   []string{"Content-Type", "Authorization", "Accept", "X-Trace-ID"},
	AllowCredentials: true,
})
```

This makes Neo usable from browser clients and future TypeScript clients without forcing unsafe production defaults.

---

## Request Format

### Query over GET

```http
GET /neo/user.get?input={"id":1}
```

### Query over POST

```http
POST /neo/user.get
Content-Type: application/json

{
  "input": {
    "id": 1
  }
}
```

### Mutation over POST

```http
POST /neo/user.create
Content-Type: application/json

{
  "input": {
    "name": "Kamil"
  }
}
```

### Subscription over GET with NDJSON

```http
GET /neo/events.feed
Accept: application/x-ndjson
```

### Subscription over WebSocket

```http
GET /neo/events.feed?input={"topic":"feed"}
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Key: <base64 nonce>
Sec-WebSocket-Version: 13
```

The server streams JSON WebSocket text frames using the same response envelope as NDJSON subscriptions.

```json
{"result":{"name":"post.created","data":{"id":1}}}
```

---

## Response Format

### Success

```json
{
  "result": {
    "message": "hello"
  }
}
```

### Error

```json
{
  "code": "NOT_FOUND",
  "error": "procedure not found"
}
```

### Subscription Stream

NDJSON subscriptions stream one JSON envelope per line. WebSocket subscriptions stream the same envelopes as text frames.

```json
{"result":{"name":"post.created","data":{"id":1}}}
{"result":{"name":"post.created","data":{"id":2}}}
{"result":{"name":"post.created","data":{"id":3}}}
```

---

## Metadata

Neo stores procedure metadata for code generation and runtime introspection.

```go
metadata := router.Metadata()
```

Example metadata:

```json
[
  {
    "key": "user.get",
    "kind": "query",
    "input": "GetUserInput",
    "output": "User",
    "summary": "Get user",
    "tags": ["users", "read"]
  },
  {
    "key": "user.create",
    "kind": "mutation",
    "input": "CreateUserInput",
    "output": "User"
  }
]
```

Attach richer metadata when registering procedures:

```go
router.Register("user.get", neo.Query(
	func(ctx context.Context, in GetUserInput) (User, error) {
		return db.GetUser(ctx, in.ID)
	},
	neo.WithSummary("Get user"),
	neo.WithDescription("Returns one user by ID."),
	neo.WithTags("users", "read"),
))
```

The same metadata is available over HTTP at the reserved metadata endpoint:

```http
GET /neo/_meta
```

Clients can fetch it without knowing the endpoint path:

```go
metadata, err := client.Metadata(context.Background())
if err != nil {
	log.Fatal(err)
}
```

Gateway metadata includes service prefixes for both mounted local routers and
proxied services configured with `neo.WithProxyMetadata`.

This is the foundation for generated typed clients.

---

## Validation

Procedure inputs can implement `Validate() error`. Neo calls it after JSON
decoding and before the handler runs. Validation errors are returned as
`BAD_REQUEST`.

```go
type CreateUserInput struct {
	Name string `json:"name"`
}

func (in CreateUserInput) Validate() error {
	if in.Name == "" {
		return errors.New("name is required")
	}
	return nil
}
```

---

## Codegen Direction

Neo ships with a small `neo-gen` command for generated clients and typed procedure bindings.

Given route registration like this:

```go
package main

import (
	"context"

	"github.com/Protocol-Lattice/neo"
)

type NoInput struct{}
type GetUserInput struct {
	ID int `json:"id"`
}
type CreateUserInput struct {
	Name string `json:"name"`
}
type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type UserEvent struct {
	Name string `json:"name"`
	Data User   `json:"data"`
}

func buildRouter() *neo.Router {
	root := neo.NewRouter()
	users := neo.NewRouter()

	root.Register("healthcheck", neo.Query[NoInput, string](func(ctx context.Context, in NoInput) (string, error) {
		return "ok", nil
	}))

	users.Register("getByID", neo.Query[GetUserInput, User](func(ctx context.Context, in GetUserInput) (User, error) {
		return User{ID: in.ID, Name: "Kamil"}, nil
	}))
	users.Register("create", neo.Mutation[CreateUserInput, User](func(ctx context.Context, in CreateUserInput) (User, error) {
		return User{ID: 1, Name: in.Name}, nil
	}))
	users.RegisterSubscription("changes", neo.Subscription[NoInput, UserEvent](func(ctx context.Context, in NoInput) (<-chan UserEvent, error) {
		out := make(chan UserEvent)
		return out, nil
	}))

	root.Nested("user", users)
	return root
}
```

Generate a typed client:

```bash
go run ./cmd/neo-gen -dir ./examples -out ./examples/neo.gen.go
```

Generate from a running server's metadata endpoint:

```bash
go run ./cmd/neo-gen \
  -metadata-url http://localhost:8080/neo/_meta \
  -package main \
  -out ./neo.gen.go
```

When `-metadata-url` is set, `neo-gen` uses procedure metadata from the server.
If `-dir` points at a local package, `neo-gen` still reads local type
declarations for richer TypeScript output and package inference.

Generate a TypeScript client:

```bash
go run ./cmd/neo-gen -dir ./examples/ts_client -out ./examples/ts_client/neo.runtime.ts -target ts-runtime
go run ./cmd/neo-gen -dir ./examples/ts_client -out ./examples/ts_client/neo.gen.ts -target ts
```

Use the generated client in application code:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Protocol-Lattice/neo"
)

func main() {
	ctx := context.Background()

	client := NewTypedClient(
		"http://localhost:8080/neo",
		neo.WithHeader("Authorization", "Bearer <token>"),
		neo.WithHeader("X-Trace-ID", "demo-1"),
	)

	health, err := client.Healthcheck.Call(ctx, NoInput{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("health:", health)

	user, err := client.User.GetByID.Call(ctx, GetUserInput{ID: 1})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("user:", user.Name)

	created, err := client.User.Create.Call(ctx, CreateUserInput{Name: "Neo"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("created:", created.ID)

	stream, err := client.User.Changes.Subscribe(ctx, NoInput{})
	if err != nil {
		log.Fatal(err)
	}

	for event := range stream {
		fmt.Printf("ndjson event: %#v\n", event)
	}

	wsStream, err := client.User.Changes.SubscribeWebSocket(ctx, NoInput{})
	if err != nil {
		log.Fatal(err)
	}

	for event := range wsStream {
		fmt.Printf("websocket event: %#v\n", event)
	}

	metadata, err := client.Metadata(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("procedures:", len(metadata))
}
```

The generated client allows usage like:

```go
user, err := client.User.GetByID.Call(ctx, GetUserInput{ID: 1})
```

Instead of:

```go
user, err := neo.CallTyped[GetUserInput, User](
	ctx,
	client.Query.Procedure("user.getByID"),
	GetUserInput{ID: 1},
)
```

The TypeScript client uses `fetch` for queries, mutations, and NDJSON subscriptions, plus browser-compatible WebSocket subscriptions:

```ts
import { createClient } from "./neo.gen";

const client = createClient("http://localhost:8080/neo", {
  headers: {
    Authorization: "Bearer <token>",
  },
});

const user = await client.user.getByID.query({ id: 1 });
const created = await client.user.create.mutate({ name: "Neo" });

for await (const event of client.user.changes.subscribe({})) {
  console.log(event.name);
}

for await (const event of client.user.changes.subscribeWebSocket({})) {
  console.log(event.name);
}

const metadata = await client.metadata();
console.log(metadata.length);
```

See `examples/ts_client` for a runnable Go server plus TypeScript client use case.

The default TypeScript target imports the shared runtime from `./neo.runtime.ts`.
Use `-target ts-runtime` to generate that runtime file, or `-target ts-standalone`
if you want one self-contained generated client file.

Custom headers are sent for `fetch` calls: queries, mutations, and NDJSON
subscriptions. Browser WebSocket constructors do not support custom request
headers, so WebSocket auth should use cookies or a server-issued token encoded
in the subscription input or URL.

Current codegen gives you:

- generated Go client namespaces
- compile-time checked input/output types
- generated Go client `Metadata(ctx)` helper
- typed query and mutation `Call` methods
- typed subscription `Subscribe` methods for NDJSON
- typed subscription `SubscribeWebSocket` methods for WebSocket streams
- `NewTypedClient(addr, opts...)` support for auth/custom headers
- `NewTypedClientFromClient(client)` for shared custom clients
- generated TypeScript local interfaces from Go structs and JSON tags
- typed TypeScript query, mutation, NDJSON subscription, and WebSocket subscription helpers
- TypeScript runtime `metadata()` helper
- TypeScript client options for custom headers, custom `fetch`, and custom `WebSocket`
- reusable generated TypeScript runtime via `-target ts-runtime`
- named TypeScript placeholders for imported Go selector types that Neo cannot inspect locally
- generated Markdown docs via `-target docs`
- generated JSON schema exports via `-target schema`

Generate Markdown docs or a JSON schema-style metadata export:

```bash
go run ./cmd/neo-gen -dir ./examples -target docs -out ./API.md
go run ./cmd/neo-gen -dir ./examples -target schema -out ./neo.schema.json
```

---

## Testing

Run tests:

```bash
go test ./...
```

Run race tests:

```bash
go test -race ./...
```

Run benchmarks:

```bash
go test -bench=. -benchmem ./...
```

Run the in-process `bufconn` example, which mounts Neo on an in-memory listener
and bypasses kernel TCP entirely:

```bash
go run ./examples/bufconn
```

Recommended full check:

```bash
go test ./... &&
go test -race ./... &&
go test -bench=. -benchmem ./...
```

---

## Performance Notes

Neo favors developer experience and type-friendly RPC ergonomics.

Internally, the generic procedure layer bridges typed Go handlers with transport-level `any` values. This can involve JSON marshal/unmarshal conversion when decoding dynamic inputs into typed structs.

That means Neo will not be faster than hand-written `net/http` handlers in raw microbenchmarks.

The goal is different:

- faster API development
- cleaner procedure organization
- fewer manual routing mistakes
- typed inputs and outputs
- generated clients
- subscription support over NDJSON and WebSocket
- good-enough performance for most app backends

For maximum performance-critical endpoints, you can still mount custom `net/http` handlers beside Neo.

---

## Production Notes

Recommended production setup:

```go
server := &http.Server{
	Addr:         ":8080",
	Handler:      mux,
	ReadTimeout:  5 * time.Second,
	WriteTimeout: 10 * time.Second,
	IdleTimeout:  60 * time.Second,
}
```

Also consider:

- reverse proxy request limits
- structured logging
- panic recovery middleware
- authentication middleware
- rate limiting
- distributed event broker with explicit delivery semantics
- graceful shutdown
- observability with metrics/tracing
- generated clients checked in CI

---

## Example Project Layout

```txt
.
├── cmd
│   └── api
│       └── main.go
├── internal
│   ├── users
│   │   ├── router.go
│   │   ├── procedures.go
│   │   └── types.go
│   ├── posts
│   │   ├── router.go
│   │   ├── procedures.go
│   │   └── types.go
│   └── events
│       └── broker.go
├── go.mod
└── go.sum
```

Example module router:

```go
package users

import (
	"context"

	"github.com/Protocol-Lattice/neo"
)

func Router() *neo.Router {
	router := neo.NewRouter()

	router.Register("get", neo.Query(Get))
	router.Register("create", neo.Mutation(Create))

	return router
}

func Get(ctx context.Context, in GetUserInput) (User, error) {
	return User{ID: in.ID, Name: "Kamil"}, nil
}

func Create(ctx context.Context, in CreateUserInput) (User, error) {
	return User{ID: 1, Name: in.Name}, nil
}
```

App router:

```go
router := neo.NewRouter()

router.Nested("user", users.Router())
router.Nested("post", posts.Router())
```

---

## Authentication Middleware Example

```go
func AuthMiddleware(next neo.Handler) neo.Handler {
	return func(ctx context.Context, input any) (any, error) {
		userID, ok := ctx.Value("user_id").(string)
		if !ok || userID == "" {
			return nil, neo.NewError(neo.CodeUnauthorized, "unauthorized")
		}

		return next(ctx, input)
	}
}
```

Usage:

```go
router.Use(AuthMiddleware)

router.Register("me", neo.Query(func(ctx context.Context, in struct{}) (User, error) {
	userID := ctx.Value("user_id").(string)
	return User{ID: userID}, nil
}))
```

See `examples/prisma_auth` for a runnable example that resolves an
`Authorization: Bearer ...` token to a user ID, stores that user ID on
`context.Context`, and uses a `neo-gen` typed client against a password-hash
Prisma auth flow with `auth.login`, `auth.register`, and protected `user.me`.

---

## Slog Middleware Example

```go
func SlogMiddleware(logger *slog.Logger) neo.Middleware {
	return func(next neo.Handler) neo.Handler {
		return func(ctx context.Context, input any) (any, error) {
			start := time.Now()

			out, err := next(ctx, input)

			attrs := []any{
				"duration", time.Since(start),
			}
			if err != nil {
				attrs = append(attrs, "error", err)
				logger.ErrorContext(ctx, "neo procedure failed", attrs...)
				return out, err
			}

			logger.InfoContext(ctx, "neo procedure completed", attrs...)
			return out, nil
		}
	}
}
```

---

## Request ID Middleware Example

```go
type requestIDKey struct{}

func RequestIDMiddleware(next neo.Handler) neo.Handler {
	var seq atomic.Uint64

	return func(ctx context.Context, input any) (any, error) {
		id := strconv.FormatUint(seq.Add(1), 10)
		ctx = context.WithValue(ctx, requestIDKey{}, id)
		return next(ctx, input)
	}
}
```

Use this with logging middleware when you need procedure-level correlation.
HTTP-level request IDs should be generated at your outer `net/http` middleware
so they can also be returned in response headers.

---

## Rate Limit Middleware Example

```go
func RateLimitMiddleware(next neo.Handler) neo.Handler {
	sem := make(chan struct{}, 64)

	return func(ctx context.Context, input any) (any, error) {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return nil, neo.NewError(neo.CodeTimeout, "request cancelled")
		default:
			return nil, neo.NewError(neo.CodeTooManyRequests, "too many requests")
		}

		return next(ctx, input)
	}
}
```

---

## Philosophy

Neo is built around a simple idea:

> Backend APIs should be organized as typed procedures, not scattered route handlers.

Go already has excellent primitives:

- `context.Context`
- `net/http`
- structs
- interfaces
- generics
- channels
- middleware-style function composition

Neo connects those primitives into a compact RPC framework.

No magic runtime required.  
No heavy dependency graph required.  
No complicated server object required.

Just routers, procedures, middleware, and typed handlers.

---

## Status

Neo is young but already has the core architecture of a serious framework:

- simple API
- good extension points
- tested router behavior
- safer production defaults
- clear path toward codegen
- WebSocket subscription transport for browser clients
- clear path toward distributed subscriptions

Use it for experiments, internal tools, and early-stage services.

For production, pair it with:

- robust auth
- proper logging
- graceful shutdown
- distributed event broker with explicit delivery semantics
- generated clients
- CI with `go test -race`

---

## License

MIT
