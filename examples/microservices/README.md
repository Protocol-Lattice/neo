# Neo Microservices Example

This example runs two independently deployed Neo services behind one Neo gateway.

- `users` owns user procedures on `:8081`.
- `orders` owns order procedures on `:8082` and calls the users service directly.
- `gateway` exposes both services from one public API on `:8080`.
- `client` calls the gateway with service-prefixed procedure keys.

Run each service in a separate terminal:

```bash
go run ./examples/microservices/users
go run ./examples/microservices/orders
go run ./examples/microservices/gateway
```

Then call through the gateway:

```bash
go run ./examples/microservices/client
```

The gateway package also has generated typed client definitions. Regenerate
them after changing gateway proxy metadata:

```bash
go run ./cmd/neo-gen -dir ./examples/microservices/gateway \
  -out ./examples/microservices/client/neo.gen.go \
  -package main
```

Equivalent curl calls:

```bash
curl 'http://localhost:8080/neo/users.getByID?input={"id":1}'

curl -X POST 'http://localhost:8080/neo/orders.create' \
  -H 'Content-Type: application/json' \
  -d '{"input":{"userID":1,"sku":"neo-sticker","quantity":2}}'
```

The gateway strips the service prefix before forwarding, so `users.getByID`
becomes `getByID` on the users service and `orders.create` becomes `create` on
the orders service.
