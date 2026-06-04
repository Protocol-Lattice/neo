# neo

Small tRPC-style Go prototype with:

- Query procedures
- Mutation procedures
- Subscription procedures over NDJSON HTTP streaming
- Router middleware chain
- Nested routers
- In-memory event bus
- Mutation-triggered subscription events
- Typed procedure helpers
- Generated typed client wrappers

## Typed server procedures

```go
app.Register("healthcheck", neo.Query[NoInput, HealthcheckOutput](func(ctx context.Context, input NoInput) (HealthcheckOutput, error) {
    return HealthcheckOutput{OK: true, Service: "neo"}, nil
}))

users.Register("create", neo.Mutation[CreateUserInput, User](func(ctx context.Context, input CreateUserInput) (User, error) {
    return User{ID: 2, Name: input.Name}, nil
}))

users.RegisterSubscription("changes", neo.Subscription[NoInput, UserEvent](func(ctx context.Context, input NoInput) (<-chan UserEvent, error) {
    return events, nil
}))
```

## Generate typed client

The generator scans calls using `neo.Query[In, Out]`, `neo.Mutation[In, Out]`, and `neo.Subscription[In, Out]`.

```bash
go run ./cmd/neo-gen -dir ./examples -out ./examples/neo.gen.go
```

Generated usage:

```go
client := NewTypedClient(server.URL + "/neo")

health, err := client.Healthcheck.Query(ctx, NoInput{})
user, err := client.User.GetByID.Query(ctx, GetUserInput{ID: 1})
created, err := client.User.Create.Mutate(ctx, CreateUserInput{Name: "Raezil"})
changes, err := client.User.Changes.Subscribe(ctx, NoInput{})
```

## Errors

Return a typed `neo.Error` to control the HTTP status and hand the client a
machine-readable code. Any other error is treated as `INTERNAL` (HTTP 500), so
existing handlers that return plain errors keep working.

```go
users.Register("get", neo.Query[GetUserInput, User](func(ctx context.Context, in GetUserInput) (User, error) {
    u, ok := store.Find(in.ID)
    if !ok {
        return User{}, neo.NewError(neo.CodeNotFound, "no such user")
    }
    return u, nil
}))
```

Codes map to statuses (`BAD_REQUEST`→400, `UNAUTHORIZED`→401, `FORBIDDEN`→403,
`NOT_FOUND`→404, `METHOD_NOT_ALLOWED`→405, `CONFLICT`→409, `TOO_MANY_REQUESTS`→429,
`INTERNAL`→500, `NOT_IMPLEMENTED`→501, `UNAVAILABLE`→503, `TIMEOUT`→504). The
client returns a `*neo.Error`, so callers can branch with `errors.As`.

## HTTP method mapping

Queries are served over `GET`, mutations over `POST`, and subscriptions over
`GET` (NDJSON stream). The router enforces this and replies `405` with an
`Allow` header on a mismatch.

## Registration contract

A `Router` is not synchronized: complete all registration before serving.

- Register every procedure (`Register`, `RegisterSubscription`, `Merge`,
  `Nested`) before the first request. The maps are unlocked for zero
  per-request overhead, so registering while serving is a data race.
- `Use` must be called **before** the procedures it should wrap. Middleware is
  snapshotted into each procedure at registration time and is not applied
  retroactively. For scoped middleware, build a sub-router, call `Use` on it,
  register into it, then attach it with `Nested`/`Merge`.

## Run

```bash
go run ./examples
```

The example uses `httptest.NewServer`, so it does not require port `8080`.
