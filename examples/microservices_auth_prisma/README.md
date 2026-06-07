# Neo Microservices Prisma Auth Example

This example runs a Prisma-backed auth service and a Prisma-backed protected
orders service behind one Neo gateway.

- `auth` owns users, bcrypt password hashes, auth tokens, and `auth.me` on `:8091`.
- `orders` owns order creation on `:8092`, persists orders with Prisma, and
  validates bearer tokens by calling the auth service.
- `gateway` exposes both services from one public API on `:8090`.
- `client` calls the gateway with a generated typed client.

The gateway forwards the caller's `Authorization: Bearer ...` header to the
orders service. The orders service resolves that token by calling `auth.me`, so
authorization stays centralized in the auth service while business procedures
can read the authenticated user from `context.Context`.

Run each service in a separate terminal:

```bash
go run ./examples/microservices_auth_prisma/auth
go run ./examples/microservices_auth_prisma/orders
go run ./examples/microservices_auth_prisma/gateway
```

Then call through the gateway:

```bash
go run ./examples/microservices_auth_prisma/client
```

Expected output:

```txt
unauthenticated orders.create: UNAUTHORIZED: unauthorized
auth.register: token=true user.email="raezil+...@example.com"
auth.login: token=true user.email="raezil+...@example.com"
auth.me: email="raezil+...@example.com" name="Raezil"
orders.create: id=... user.email="raezil+...@example.com" sku="neo-sticker" quantity=2
```

The Prisma schema lives at
`examples/microservices_auth_prisma/prisma/schema.prisma` and defines users,
auth tokens, and orders. Both services use the generated Prisma Client Go
package in `examples/microservices_auth_prisma/db` and the local SQLite
database at `examples/microservices_auth_prisma/prisma/dev.db`.

Regenerate the Prisma client after changing the schema:

```bash
go run github.com/steebchen/prisma-client-go generate \
  --schema ./examples/microservices_auth_prisma/prisma/schema.prisma
```

The gateway package has generated typed client definitions. Regenerate them
after changing gateway proxy metadata:

```bash
go run ./cmd/neo-gen -dir ./examples/microservices_auth_prisma/gateway \
  -out ./examples/microservices_auth_prisma/client/neo.gen.go \
  -package main
```

Equivalent curl calls:

```bash
curl -X POST 'http://localhost:8090/neo/auth.register' \
  -H 'Content-Type: application/json' \
  -d '{"input":{"email":"raezil@example.com","name":"Raezil","password":"secret123"}}'

curl 'http://localhost:8090/neo/auth.login?input={"user":"kamil@example.com","password":"secret123"}'

curl -X POST 'http://localhost:8090/neo/orders.create' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer demo-token' \
  -d '{"input":{"sku":"neo-sticker","quantity":2}}'
```
