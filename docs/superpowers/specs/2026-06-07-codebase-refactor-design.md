# Codebase Refactor Design

## Purpose

Refactor the Neo codebase to improve maintainability without changing behavior or
public APIs. The work focuses on splitting oversized implementation files by
responsibility while preserving existing package boundaries, command flags,
generated output semantics, tests, and runtime behavior.

## Scope

This is a behavior-preserving structural refactor.

Included:

- Split `cmd/neo-gen/command.go` into smaller files grouped by scanner,
  metadata, generation target, and naming/type helpers.
- Split `binary.go` into smaller files grouped by codec facade, encoding,
  decoding, assignment/conversion, and struct/tag helpers.
- Apply small local extractions in runtime files only when they directly reduce
  coupling and are backed by existing tests.
- Keep generated files and generated example artifacts out of manual edits.

Excluded:

- Public API changes in `github.com/Protocol-Lattice/neo`.
- Command-line flag changes for `cmd/neo-gen`.
- Package or module restructuring.
- Manual rewrites of `*.gen.go` or Prisma-generated files.
- New runtime dependencies.

## Current Context

The repository is a Go workspace targeting Go 1.22 with these modules:

- Root library module: `github.com/Protocol-Lattice/neo`.
- Examples module under `examples`.
- Benchmarks module under `benchmarks`.

The main maintainability pressure points are:

- `cmd/neo-gen/command.go`, which mixes AST scanning, metadata handling, Go
  generation, TypeScript generation, Markdown docs, JSON schema output, naming,
  and type conversion helpers in one file.
- `binary.go`, which mixes public codec definitions, reflection-based encoding,
  decoding, assignment, scalar conversion, struct field discovery, tag parsing,
  and map-key conversion in one file.

Existing verification covers `gofmt`, `go vet`, normal tests, and race tests in
CI. There is no current `golangci-lint` configuration, so lint adoption is not
part of this refactor.

## Architecture

The package layout stays unchanged.

Root `neo` package:

- Remains the public runtime package.
- Keeps exported names and behavior stable.
- Receives file-level splits only; all binary codec helpers remain unexported in
  the same package.

`cmd/neo-gen`:

- Remains `package main`.
- Keeps the command entrypoint and flags in `main.go`.
- Splits implementation files by pipeline responsibility while preserving
  package-private helper access.

Broker packages:

- Stay unchanged unless formatting or existing verification requires a local
  adjustment.

## Planned File Boundaries

`cmd/neo-gen` target layout:

- `main.go`: command parsing, metadata URL loading, target dispatch, file output.
- `scan.go`: package scanning, type declaration discovery, register call
  discovery, nested router prefix inference, and AST expression stringification.
- `metadata.go`: procedure metadata extraction from procedure options and
  gateway proxy metadata literals.
- `generate_go.go`: Go typed client generation.
- `generate_ts.go`: TypeScript runtime and client generation.
- `generate_docs.go`: Markdown docs and JSON schema generation.
- `names.go`: shared sorting, grouping, export-name, identifier, and TypeScript
  naming helpers.

Root binary codec target layout:

- `binary.go`: exported content type, `BinaryCodec`, `NeoBinaryCodec`, binary
  magic bytes, and kind constants.
- `binary_encode.go`: `writeBinaryValue` and primitive/list/map/struct write
  helpers.
- `binary_decode.go`: `binaryValueDecoder` and primitive read helpers.
- `binary_assign.go`: decoded-value assignment, scalar conversion, text/binary
  unmarshaler handling, and map-key assignment.
- `binary_fields.go`: struct field discovery, JSON tag parsing, empty-value
  checks, map-key stringification, and decoded `any` normalization.

These boundaries keep related code together without introducing new packages or
new exported surface area.

## Data Flow

`neo-gen` data flow remains unchanged:

1. Parse command flags.
2. Load procedure metadata from either local Go package scanning or a remote
   metadata endpoint.
3. Normalize discovered procedures and package/type information.
4. Dispatch to the requested target generator.
5. Write the generated output file.

Binary codec data flow remains unchanged:

1. `BinaryCodec.Marshal` writes magic bytes and encodes a reflected Go value as
   the current TLV stream.
2. `BinaryCodec.Unmarshal` validates magic bytes, decodes one TLV value, checks
   for trailing bytes, and assigns the decoded value into a non-nil pointer.
3. Existing reflection rules for structs, tags, maps, slices, arrays, scalar
   conversions, and text/binary marshalers continue to apply.

## Error Handling

The refactor preserves externally visible errors.

- Existing error messages remain unchanged where tests assert or callers may
  depend on them.
- Helper extraction must return errors through the same call paths as before.
- No new panics are introduced.
- No new logging or process exits are introduced outside the existing command
  entrypoint behavior.

## Testing Strategy

Verification uses the existing green baseline as the reference.

During implementation:

- Move code in small chunks.
- Run focused tests for `cmd/neo-gen` after generator splits.
- Run focused root package tests after binary codec splits.
- Add targeted regression tests only if extraction reveals behavior that is not
  currently constrained by tests.

Final verification:

- `gofmt` on changed Go files.
- `go test ./...` in the root module.
- `go test ./...` in `examples`.
- `go test ./...` in `benchmarks`.
- `go vet ./...` in the root module.
- `go test -race ./...` in the root module.

## Success Criteria

- All final verification commands pass.
- Public APIs and command flags remain compatible.
- Generated output behavior remains equivalent.
- `cmd/neo-gen/command.go` no longer carries unrelated scanner, generator,
  metadata, and naming responsibilities in one file.
- `binary.go` becomes a small public facade while binary internals live in
  focused files.
- Git diff is dominated by moves and narrow extractions, not unrelated behavior
  changes.
