# Neo

[![Go Reference](https://pkg.go.dev/badge/github.com/Protocol-Lattice/neo.svg)](https://pkg.go.dev/github.com/Protocol-Lattice/neo)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Protocol-Lattice/neo)](https://go.dev/)
[![License](https://img.shields.io/github/license/Protocol-Lattice/neo)](./LICENSE)

<p align="center">
  <img width="180" height="180" alt="Neo logo" src="https://github.com/user-attachments/assets/8f0fce3d-d34b-4771-afc1-7d0ea4c5d533" />
</p>

Neo is a lightweight, type-friendly RPC framework for Go. It gives Go services
a tRPC-style procedure API on top of `net/http`: queries, mutations,
subscriptions, middleware, generated clients, metadata, and gateway routing.

Use Neo when you want typed backend procedures without switching to a large
framework or leaving ordinary Go primitives like `context.Context`, structs,
interfaces, channels, and HTTP middleware.

## Contents

- [Install](#install)
- [Quick Start](#quick-start)
- [Client Calls](#client-calls)
- [Core Concepts](#core-concepts)
- [Subscriptions](#subscriptions)
- [Middleware](#middleware)
- [Validation](#validation)
- [Metadata and Code Generation](#metadata-and-code-generation)
- [Gateways and Microservices](#gateways-and-microservices)
- [Event Brokers](#event-brokers)
- [HTTP API](#http-api)
- [Production Notes](#production-notes)
- [Examples](#examples)
- [Testing](#testing)

## Install

```sh
go get github.com/Protocol-Lattice/neo
```

To install the generator:

```sh
go install github.com/Protocol-Lattice/neo/cmd/neo-gen@latest
```

Inside this repository, examples use:

```sh
go run ./cmd/neo-gen
```

## Quick Start

Create a router, register typed procedures, and serve it under an HTTP prefix.

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

Call the query:

```sh
curl --get 'http://localhost:8080/neo/hello' \
  --data-urlencode 'input={"name":"Neo"}'
```

Response:

```json
{
  "result": {
    "message": "Hello, Neo"
  }
}
```

## Client Calls

Neo ships with a small Go client. Use `CallTyped` when you want typed inputs and
outputs without generated code.

```go
client := neo.NewClient(
	"http://localhost:8080/neo",
	neo.WithHeader("Authorization", "Bearer <token>"),
)

out, err := neo.CallTyped[HelloInput, HelloOutput](
	ctx,
	client.Query.Procedure("hello"),
	HelloInput{Name: "Kamil"},
)
if err != nil {
	return err
}

fmt.Println(out.Message)
```

For Go-to-Go unary calls, clients can opt into Neo's compact binary envelope:

```go
client := neo.NewClient("http://localhost:8080/neo", neo.WithBinaryCodec())
```

`WithBinaryCodec` applies to unary Go client queries and mutations. Metadata,
NDJSON subscriptions, and WebSocket subscriptions stay JSON-based for browser
and streaming compatibility.

## Core Concepts

### Queries

Queries are read-style procedures. They are served over `GET` by default and can
also be called over `POST` for larger request bodies.

```go
type GetUserInput struct {
	ID int `json:"id"`
}

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

router.Register("user.get", neo.Query(func(ctx context.Context, in GetUserInput) (User, error) {
	return db.GetUser(ctx, in.ID)
}))
```

```sh
curl --get 'http://localhost:8080/neo/user.get' \
  --data-urlencode 'input={"id":1}'
```

```sh
curl -X POST 'http://localhost:8080/neo/user.get' \
  -H 'Content-Type: application/json' \
  -d '{"input":{"id":1}}'
```

### Mutations

Mutations are write-style procedures and are served over `POST`.

```go
type CreateUserInput struct {
	Name string `json:"name"`
}

router.Register("user.create", neo.Mutation(func(ctx context.Context, in CreateUserInput) (User, error) {
	return db.CreateUser(ctx, in.Name)
}))
```

```sh
curl -X POST 'http://localhost:8080/neo/user.create' \
  -H 'Content-Type: application/json' \
  -d '{"input":{"name":"Kamil"}}'
```

### Nested Routers

Use nested routers to keep feature modules small while exposing one procedure
tree.

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

Registered procedure keys:

```txt
user.get
user.create
```

Use `Merge` when you want to compose routers without adding a prefix.

```go
app := neo.NewRouter()
app.Merge(authRouter)
app.Merge(billingRouter)
```

## Subscriptions

Subscriptions expose server-pushed events from typed Go handlers. Neo supports:

- NDJSON over HTTP, which is curl-friendly and the default subscription client
  transport.
- WebSocket streams, which are useful for browser and real-time clients.

```go
router.RegisterSubscription("events.feed", neo.Subscription(func(ctx context.Context, in struct{}) (<-chan any, error) {
	return router.Events().Subscribe(ctx, "feed"), nil
}))
```

Publish from a mutation:

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

Use NDJSON from the Go client:

```go
stream, err := client.Subscription.Procedure("events.feed").Subscribe(ctx, nil)
if err != nil {
	log.Fatal(err)
}

for event := range stream {
	fmt.Printf("event: %#v\n", event)
}
```

Use WebSocket from the Go client:

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
```

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

Register middleware before registering the procedures it should wrap:

```go
router := neo.NewRouter()
router.Use(LoggingMiddleware)

router.Register("hello", neo.Query(func(ctx context.Context, in HelloInput) (HelloOutput, error) {
	return HelloOutput{Message: "hello " + in.Name}, nil
}))
```

Middleware is snapshotted at registration time. Calling `Use` after a procedure
is registered does not retroactively wrap that procedure. For scoped middleware,
build a sub-router, call `Use` on it, register procedures into it, then attach
it with `Nested` or `Merge`.

### Authentication Example

```go
type userIDKey struct{}

func AuthMiddleware(next neo.Handler) neo.Handler {
	return func(ctx context.Context, input any) (any, error) {
		userID, ok := ctx.Value(userIDKey{}).(string)
		if !ok || userID == "" {
			return nil, neo.NewError(neo.CodeUnauthorized, "unauthorized")
		}

		return next(ctx, input)
	}
}
```

See `examples/prisma_auth` for a runnable password-hash auth flow with
`auth.register`, `auth.login`, bearer-token middleware, and protected `user.me`.
See `examples/microservices_auth_prisma` for the same flow split across auth,
orders, gateway, and client commands.

## Validation

Procedure inputs can implement `Validate() error`. Neo calls it after decoding
and before the handler runs. Validation failures are returned as `BAD_REQUEST`.

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

## Metadata and Code Generation

Neo stores procedure metadata for introspection, generated clients, generated
Markdown docs, and schema exports.

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

Read metadata in Go:

```go
metadata := router.Metadata()
```

Fetch metadata over HTTP:

```http
GET /neo/_meta
```

Fetch metadata from the Go client:

```go
metadata, err := client.Metadata(context.Background())
```

Generate a typed Go client from local procedure registrations:

```sh
go run ./cmd/neo-gen -dir ./examples -out ./examples/neo.gen.go
```

Generate from a running server metadata endpoint:

```sh
go run ./cmd/neo-gen \
  -metadata-url http://localhost:8080/neo/_meta \
  -package main \
  -out ./neo.gen.go
```

Generated Go clients provide namespaced typed procedure helpers:

```go
client := NewTypedClient("http://localhost:8080/neo", neo.WithBinaryCodec())

user, err := client.User.GetByID.Query(ctx, GetUserInput{ID: 1})
created, err := client.User.Create.Mutate(ctx, CreateUserInput{Name: "Neo"})
stream, err := client.User.Changes.Subscribe(ctx, NoInput{})
wsStream, err := client.User.Changes.SubscribeWebSocket(ctx, NoInput{})
metadata, err := client.Metadata(ctx)
```

Generate a TypeScript runtime plus TypeScript client:

```sh
go run ./cmd/neo-gen -dir ./examples/ts_client \
  -out ./examples/ts_client/neo.runtime.ts \
  -target ts-runtime

go run ./cmd/neo-gen -dir ./examples/ts_client \
  -out ./examples/ts_client/neo.gen.ts \
  -target ts
```

Use the generated TypeScript client:

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

The default TypeScript target imports a shared runtime from `./neo.runtime.ts`.
Use `-target ts-runtime` for that runtime file, or `-target ts-standalone` for
one self-contained generated client.

Custom headers are sent for TypeScript `fetch` calls: queries, mutations, and
NDJSON subscriptions. Browser WebSocket constructors do not allow custom
request headers, so WebSocket auth should use cookies or a server-issued token
encoded in the subscription input or URL.

Generate Markdown docs or schema-style metadata:

```sh
go run ./cmd/neo-gen -dir ./examples -target docs -out ./API.md
go run ./cmd/neo-gen -dir ./examples -target schema -out ./neo.schema.json
```

`neo-gen` can generate:

- Go typed client namespaces.
- Go typed query, mutation, NDJSON subscription, and WebSocket subscription
  helpers.
- Go `NewTypedClient(addr, opts...)` and `NewTypedClientFromClient(client)`.
- TypeScript interfaces from local Go structs and JSON tags.
- TypeScript query, mutation, NDJSON subscription, and WebSocket helpers.
- TypeScript runtime files or standalone clients.
- Markdown procedure docs.
- JSON schema-style procedure metadata exports.

## Gateways and Microservices

Use `neo.NewGateway()` when several Neo services should share one public API.
A gateway can mount local routers or proxy remote services.

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

The gateway forwards `users.getByID` to `getByID` on the users service.

Gateway-only packages can still use `neo-gen`. Add proxy metadata manually:

```go
gateway.Proxy(
	"users",
	"http://localhost:8081/neo",
	neo.WithProxyMetadata(neo.ProcedureMeta{
		Key:    "getByID",
		Kind:   neo.ProcedureKindQuery,
		Input:  "GetUserInput",
		Output: "User",
	}),
)
```

Or discover metadata from upstream Neo services:

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

`WithProxyMetadataDiscovery` fetches `/_meta` when the proxy is configured.
Headers set with `WithProxyHeader` or `WithProxyHeaders` are sent with that
metadata request.

Gateway diagnostics:

```http
GET /neo/_health
```

The response lists configured services, whether each one is mounted locally or
proxied, and the metadata currently known for that service.

See `examples/microservices` for separate users, orders, gateway, and client
commands. See `examples/microservices_auth_prisma` for a gateway that forwards
bearer auth to a Prisma-backed auth service and a protected orders service.

## Event Brokers

Neo uses an in-memory event bus by default. It is useful for examples, tests,
local apps, and single-process deployments.

```go
type EventBroker interface {
	Publish(topic string, event any)
	Subscribe(ctx context.Context, topic string) <-chan any
}
```

Use a custom broker:

```go
router := neo.NewRouter()
router.UseEvents(myBroker)
```

The repository includes standard-library Redis and NATS adapters:

```go
import natsbroker "github.com/Protocol-Lattice/neo/broker/nats"

broker := natsbroker.New(natsbroker.Options{
	Addr:             "127.0.0.1:4222",
	SubscriberBuffer: 64,
	Logger:           log.Default(),
})
router.UseEvents(broker)
```

```go
import redisbroker "github.com/Protocol-Lattice/neo/broker/redis"

broker := redisbroker.New(redisbroker.Options{
	Addr:             "127.0.0.1:6379",
	SubscriberBuffer: 64,
	Logger:           log.Default(),
})
router.UseEvents(broker)
```

Event delivery is best-effort by default. `Publish` is fire-and-forget, slow
local subscribers drop events when their buffer is full, and adapter publish or
subscription errors are logged through the configured logger. For durable
delivery, retries, or outbox semantics, wrap `EventBroker` with
application-specific persistence.

## HTTP API

### Request Formats

```http
GET /neo/user.get?input={"id":1}
```

```http
POST /neo/user.get
Content-Type: application/json

{
  "input": {
    "id": 1
  }
}
```

```http
POST /neo/user.create
Content-Type: application/json

{
  "input": {
    "name": "Kamil"
  }
}
```

```http
GET /neo/events.feed
Accept: application/x-ndjson
```

```http
GET /neo/events.feed?input={"topic":"feed"}
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Key: <base64 nonce>
Sec-WebSocket-Version: 13
```

### Response Formats

Success:

```json
{
  "result": {
    "message": "hello"
  }
}
```

Error:

```json
{
  "code": "NOT_FOUND",
  "error": "procedure not found"
}
```

Subscription streams send one envelope per event. NDJSON sends one JSON object
per line; WebSocket subscriptions send the same envelopes as text frames.

```json
{"result":{"name":"post.created","data":{"id":1}}}
{"result":{"name":"post.created","data":{"id":2}}}
```

### Error Handling

Return structured errors when the client should see a specific code:

```go
return User{}, neo.NewError(neo.CodeNotFound, "user not found")
```

Supported codes:

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

Plain Go errors are logged server-side and returned as safe internal errors:

```json
{
  "code": "INTERNAL",
  "error": "internal server error"
}
```

### CORS

Neo supports browser preflight requests. By default, CORS reflects the request
`Origin` for local development. In production, configure allowed origins
explicitly.

```go
router.UseCORS(neo.CORSOptions{
	AllowedOrigins:   []string{"https://app.example.com"},
	AllowedHeaders:   []string{"Content-Type", "Authorization", "Accept", "X-Trace-ID"},
	AllowCredentials: true,
})
```

### Server Options

Use `ListenAndServe` for the built-in hardened server:

```go
err := router.ListenAndServe(neo.ServerOptions{
	Addr:           ":8080",
	Prefix:         "/neo/",
	ReadTimeout:    5 * time.Second,
	WriteTimeout:   10 * time.Second,
	IdleTimeout:    60 * time.Second,
	MaxRequestBody: 1 << 20,
})
```

Or mount Neo into your own mux:

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
```

## Production Notes

Neo routers should be built before serving. Register procedures, subscriptions,
middleware, CORS, and event brokers during setup, then treat the router as
read-only once it is handed to `net/http`.

Recommended production concerns:

- Explicit `http.Server` timeouts.
- Reverse proxy request limits.
- Authentication middleware.
- Panic recovery middleware.
- Rate limiting.
- Structured logging.
- Metrics and tracing.
- Graceful shutdown.
- Distributed event broker with delivery semantics that match your product.
- Generated clients checked in CI.

Neo's generic procedure layer bridges typed Go handlers with transport-level
values. That can involve conversion work that hand-written `net/http` handlers
avoid. For performance-critical endpoints, mount custom HTTP handlers beside
Neo.

## Examples

Run the main in-process example:

```sh
go run ./examples
```

Run the `bufconn` example, which mounts Neo on an in-memory listener:

```sh
go run ./examples/bufconn
```

Run the microservices example in separate terminals:

```sh
go run ./examples/microservices/users
go run ./examples/microservices/orders
go run ./examples/microservices/gateway
go run ./examples/microservices/client
```

Run the Prisma auth example:

```sh
go run ./examples/prisma_auth
```

Run the Go server plus TypeScript client:

```sh
go run ./examples/ts_client
node --experimental-transform-types ./examples/ts_client/client.ts
```

From `examples/ts_client`, you can also run:

```sh
npm run gen
npm run check
npm run typecheck
```

`npm run typecheck` requires installing TypeScript dependencies first.

## Testing

Run the repository checks:

```sh
go test ./...
go vet ./...
```

Run race tests:

```sh
go test -race ./...
```

Run benchmarks:

```sh
go test -bench=. -benchmem ./...
```

## Contributing

Keep changes small and covered by tests. Run `gofmt`, `go test ./...`, and
`go vet ./...` before sending changes.

## License

Neo is licensed under the terms in [LICENSE](./LICENSE).
