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

## Run

```bash
go run ./examples
```

The example uses `httptest.NewServer`, so it does not require port `8080`.
