# TypeScript Client With Go Server

This example uses the generated TypeScript client in `neo.gen.ts` against a
real Go Neo server.

From the repository root, run the server:

```bash
go run ./examples/ts_client
```

In another terminal from the repository root, run the TypeScript client:

```bash
node --experimental-transform-types ./examples/ts_client/client.ts
```

If your shell is already inside `examples/ts_client`, use the local paths:

```bash
go run .
node --experimental-transform-types client.ts
```

Or use the package scripts from `examples/ts_client`:

```bash
npm run server
npm run client
```

Regenerate the TypeScript client from the Go server in this directory:

```bash
go run ./cmd/neo-gen -dir ./examples/ts_client -out ./examples/ts_client/neo.runtime.ts -target ts-runtime
go run ./cmd/neo-gen -dir ./examples/ts_client -out ./examples/ts_client/neo.gen.ts -target ts
```

From inside `examples/ts_client`, the equivalent command is:

```bash
npm run gen
```

Check the TypeScript syntax with Node:

```bash
npm run check
```

For full TypeScript type checking, install dependencies once and run:

```bash
npm install
npm run typecheck
```

The generated client sends custom headers on `fetch` calls, including queries,
mutations, and NDJSON subscriptions. Browser WebSocket constructors do not
allow custom request headers, so WebSocket auth should use cookies or a
server-issued token encoded in the subscription input or URL.

This TypeScript client intentionally uses JSON. Neo's `application/x-neo-bin`
codec is currently a Go client option for unary Go-to-Go calls.

The client calls `healthcheck`, reads `user.getByID`, opens a `user.changes`
NDJSON subscription, creates a user through `user.create`, and receives the
published subscription event.
