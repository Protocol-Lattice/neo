# Prisma Auth Example

This example shows a protected Neo query that reads the authenticated user ID
from `context.Context`. It uses
`github.com/steebchen/prisma-client-go` with a SQLite datasource.

The auth flow is:

1. `auth.register` stores a user with a bcrypt `passwordHash` and returns an auth token.
2. `auth.login` accepts `user` and `password`, verifies the stored password
   hash, and returns an auth token.
3. `Authorization: Bearer ...` is read by HTTP middleware.
4. The token is resolved to a user ID through a Prisma-shaped database boundary.
5. The user ID is stored on the request context.
6. Neo middleware protects `user.me`.
7. The query uses the context user ID to load the current user.

Run it from the repository root:

```bash
go run ./examples/prisma_auth
```

Expected output:

```txt
unauthenticated user.me: UNAUTHORIZED: unauthorized
auth.register: token=true user.email="raezil@example.com"
auth.login: token=true user.email="kamil@example.com"
authenticated user.me: email="raezil@example.com" name="Raezil"
```

The example creates the local SQLite tables at startup so it can be run from
the repository root or from `examples/prisma_auth`.

Regenerate the Prisma Client Go package after changing `prisma/schema.prisma`:

```bash
go run github.com/steebchen/prisma-client-go generate --schema ./examples/prisma_auth/prisma/schema.prisma
```

The typed client in `neo.gen.go` was generated with:

```bash
go run ./cmd/neo-gen -dir ./examples/prisma_auth -out ./examples/prisma_auth/neo.gen.go
```

The example uses it as:

```go
client := NewTypedClient(server.URL+"/neo", neo.WithBinaryCodec())
registered, err := client.Auth.Register.Mutate(ctx, RegisterInput{
	Email:    "raezil@example.com",
	Name:     "Raezil",
	Password: "secret123",
})

authenticated := NewTypedClient(
	server.URL+"/neo",
	neo.WithBinaryCodec(),
	neo.WithHeader("Authorization", "Bearer "+registered.Token),
)
me, err := authenticated.User.Me.Query(ctx, NoInput{})
```

The `neo.WithBinaryCodec()` option makes these unary Go client calls use
`application/x-neo-bin`; the HTTP middleware and authorization behavior are the
same as JSON requests.

It also includes a public login query:

```go
login, err := client.Auth.Login.Query(ctx, LoginInput{
	User:     "kamil@example.com",
	Password: "secret123",
})
```

Protected queries can then use the returned token:

```go
client := NewTypedClient(
	server.URL+"/neo",
	neo.WithBinaryCodec(),
	neo.WithHeader("Authorization", "Bearer "+login.Token),
)
me, err := client.User.Me.Query(ctx, NoInput{})
```
