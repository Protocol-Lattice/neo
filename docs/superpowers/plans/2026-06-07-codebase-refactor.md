# Codebase Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split Neo's oversized generator and binary codec implementation files into focused files without changing behavior or public APIs.

**Architecture:** Keep the existing Go package layout. `cmd/neo-gen` remains `package main`; the root runtime remains package `neo`; all moved helpers stay unexported in their current packages. This is a mechanical responsibility split with focused verification after each slice.

**Tech Stack:** Go 1.22 workspace, standard library only, existing `go test`, `go vet`, `gofmt`, and race-test workflow.

---

## Execution Mode

Use `superpowers:executing-plans` inline. Subagent tools are available in this environment, but their policy requires the user to explicitly request subagents or parallel delegation; the user requested Superpowers implementation, not subagent delegation.

## File Structure

### `cmd/neo-gen`

- Modify: `cmd/neo-gen/command.go`
  - Keep only shared model types if needed during the first split, then reduce to no content or remove it once all responsibilities move.
- Create: `cmd/neo-gen/scan.go`
  - Own package scanning, type declaration collection, register/proxy/nested call discovery, prefix inference, and AST expression stringification.
- Create: `cmd/neo-gen/metadata.go`
  - Own procedure metadata extraction from procedure options and `neo.WithProxyMetadata` literals.
- Create: `cmd/neo-gen/generate_go.go`
  - Own Go typed client generation and Go procedure method generation.
- Create: `cmd/neo-gen/generate_ts.go`
  - Own TypeScript generated runtime, external type references, TypeScript client output, and Go-to-TypeScript type conversion.
- Create: `cmd/neo-gen/generate_docs.go`
  - Own Markdown docs and JSON schema generation.
- Create: `cmd/neo-gen/names.go`
  - Own shared sorting, grouping, Go export naming, TypeScript identifier/name helpers, and key naming helpers.

### Root `neo` Package

- Modify: `binary.go`
  - Keep `BinaryContentType`, magic bytes, kind constants, `BinaryCodec`, `NeoBinaryCodec`, and `Marshal`/`Unmarshal`.
- Create: `binary_encode.go`
  - Own `writeBinaryValue`, write helpers, and zig-zag encoding.
- Create: `binary_decode.go`
  - Own `binaryValueDecoder`, read helpers, and zig-zag decoding.
- Create: `binary_assign.go`
  - Own decoded-value assignment, scalar conversion, text/binary unmarshaler handling, and map-key assignment.
- Create: `binary_fields.go`
  - Own binary object/field model types, struct field discovery, JSON tag parsing, empty-value detection, string-tagged values, map-key stringification, and decoded `any` normalization.

---

### Task 1: Verify Baseline And Isolation

**Files:**
- Read: `go.mod`
- Read: `go.work`
- Read: `docs/superpowers/specs/2026-06-07-codebase-refactor-design.md`

- [ ] **Step 1: Confirm workspace state**

Run:

```bash
git status --short
git branch --show-current
```

Expected: clean or only the new plan file before edits; branch must not contain unrelated code changes.

- [ ] **Step 2: Confirm worktree isolation**

Run:

```bash
GIT_DIR=$(cd "$(git rev-parse --git-dir)" 2>/dev/null && pwd -P)
GIT_COMMON=$(cd "$(git rev-parse --git-common-dir)" 2>/dev/null && pwd -P)
git rev-parse --show-superproject-working-tree 2>/dev/null
printf 'git_dir=%s\ngit_common=%s\n' "$GIT_DIR" "$GIT_COMMON"
```

Expected: if already in a linked worktree, continue. If not, ask before creating one; if the user declines or sandbox blocks creation, continue in place only if the worktree is clean.

- [ ] **Step 3: Run focused baseline tests**

Run:

```bash
go test ./... ./cmd/neo-gen
```

Expected: all root package tests and generator tests pass before refactoring.

---

### Task 2: Split `cmd/neo-gen` Scanner And Metadata Code

**Files:**
- Modify: `cmd/neo-gen/command.go`
- Create: `cmd/neo-gen/scan.go`
- Create: `cmd/neo-gen/metadata.go`
- Create: `cmd/neo-gen/names.go`
- Test: `cmd/neo-gen/main_test.go`

- [ ] **Step 1: Move scanner definitions into `scan.go`**

Move the existing declarations from `cmd/neo-gen/command.go` into `cmd/neo-gen/scan.go` without changing their bodies:

```go
type procedure struct {
	Receiver    string
	Key         string
	Kind        string
	Input       string
	Output      string
	Summary     string
	Description string
	Tags        []string
	Deprecated  bool
}

type nestedRouter struct {
	Parent string
	Prefix string
	Child  string
}

type typeDeclaration struct {
	Name       string
	Type       ast.Expr
	TypeParams []string
}

type packageScan struct {
	Package    string
	Procedures []procedure
	Types      []typeDeclaration
}

func scanDir(dir string) (string, []procedure, error)
func scanPackage(dir string) (packageScan, error)
func typeDeclarations(file *ast.File) []typeDeclaration
func typeParamNames(params *ast.FieldList) []string
func registerCall(call *ast.CallExpr) (receiver, key, kind, input, output string, ok bool)
func registerCallProcedure(call *ast.CallExpr) (procedure, bool)
func routerPrefixCall(call *ast.CallExpr) (parent, prefix, child string, ok bool)
func nestedCall(call *ast.CallExpr) (parent, prefix, child string, ok bool)
func gatewayProxyCall(call *ast.CallExpr) (receiver string, procedures []procedure, ok bool)
func inferPrefixes(receivers map[string]struct{}, nested []nestedRouter) (map[string]string, error)
func fullProcedureKey(prefix, key string) string
func joinKey(left, right string) string
func typedProcedure(expr ast.Expr) (kind string, input string, output string, ok bool)
func typedProcedureMetadata(expr ast.Expr) (kind string, input string, output string, meta procedure, ok bool)
func genericCall(expr ast.Expr) (string, []ast.Expr, bool)
func selectorName(expr ast.Expr) (string, bool)
func selectorReceiverName(expr ast.Expr) (string, bool)
func stringLiteral(expr ast.Expr) (string, bool)
func boolLiteral(expr ast.Expr) (bool, bool)
func stringSliceLiteral(expr ast.Expr) ([]string, bool)
func exprString(expr ast.Expr) string
```

Expected imports for `scan.go`:

```go
import (
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
)
```

- [ ] **Step 2: Move metadata extraction into `metadata.go`**

Move the existing metadata declarations without changing their bodies:

```go
func proxyMetadataProcedures(receiver string, prefix string, expr ast.Expr) []procedure
func procedureMeta(receiver string, prefix string, expr ast.Expr) (procedure, bool)
func procedureMetaLiteral(lit *ast.CompositeLit) procedure
func procedureKind(expr ast.Expr) (string, bool)
func procedureOptions(args []ast.Expr) procedure
func mergeProcedureMetadata(target *procedure, source procedure)
```

Expected imports for `metadata.go`:

```go
import "go/ast"
```

- [ ] **Step 3: Move shared naming helpers into `names.go`**

Move the existing shared naming declarations without changing behavior:

```go
func groupProcedures(procedures []procedure) map[string][]procedure
func sortedKeys[V any](m map[string]V) []string
func procTypeName(p procedure) string
func lastKeyPart(key string) string
func exportName(s string) string
```

Expected imports for `names.go`:

```go
import (
	"slices"
	"strings"
	"unicode"
)
```

- [ ] **Step 4: Format and test generator package**

Run:

```bash
gofmt -w cmd/neo-gen/command.go cmd/neo-gen/scan.go cmd/neo-gen/metadata.go cmd/neo-gen/names.go
go test ./cmd/neo-gen
```

Expected: package builds and all generator tests pass.

---

### Task 3: Split `cmd/neo-gen` Generation Targets

**Files:**
- Modify: `cmd/neo-gen/command.go`
- Modify: `cmd/neo-gen/names.go`
- Create: `cmd/neo-gen/generate_go.go`
- Create: `cmd/neo-gen/generate_ts.go`
- Create: `cmd/neo-gen/generate_docs.go`
- Test: `cmd/neo-gen/main_test.go`

- [ ] **Step 1: Move Go generation into `generate_go.go`**

Move the existing Go generation declarations without changing their bodies:

```go
func generate(pkg string, procedures []procedure) ([]byte, error)
func writeProcedureType(b *bytes.Buffer, p procedure)
```

Expected imports for `generate_go.go`:

```go
import (
	"bytes"
	"fmt"
	"go/format"
	"strings"
)
```

- [ ] **Step 2: Move docs and schema generation into `generate_docs.go`**

Move the existing docs/schema declarations without changing their bodies:

```go
func generateDocs(procedures []procedure) ([]byte, error)
type schemaDocument struct {
	Schema     string            `json:"schema"`
	Procedures []schemaProcedure `json:"procedures"`
}

type schemaProcedure struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Input       string   `json:"input"`
	Output      string   `json:"output"`
	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Deprecated  bool     `json:"deprecated,omitempty"`
}
func generateSchema(procedures []procedure) ([]byte, error)
func sortedProcedures(procedures []procedure) []procedure
```

Expected imports for `generate_docs.go`:

```go
import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)
```

- [ ] **Step 3: Move TypeScript generation into `generate_ts.go`**

Move the existing TypeScript generation declarations without changing their bodies:

```go
type typeScriptOptions struct {
	RuntimeImport string
	Standalone    bool
}
func generateTypeScript(procedures []procedure, types []typeDeclaration, opts typeScriptOptions) ([]byte, error)
func generateTypeScriptRuntime() ([]byte, error)
func writeTypeScriptRuntimeImport(b *bytes.Buffer, runtimeImport string)
func writeTypeScriptRuntime(b *bytes.Buffer)
type externalTypeRef struct {
	Name  string
	Arity int
}
func collectExternalTypeRefs(procedures []procedure, types []typeDeclaration) []externalTypeRef
func collectProcedureExternalTypeRefsFromTypeString(typeName string, refs map[string]int, declared map[string]struct{})
func collectProcedureExternalTypeRefsFromExpr(expr ast.Expr, refs map[string]int, declared map[string]struct{})
func isExternalProcedureIdent(name string, declared map[string]struct{}) bool
func isBuiltInGoIdent(name string) bool
func collectExternalTypeRefsFromTypeString(typeName string, refs map[string]int)
func collectExternalTypeRefsFromExpr(expr ast.Expr, refs map[string]int)
func recordExternalTypeRef(refs map[string]int, name string, arity int)
func writeTypeScriptExternalTypes(b *bytes.Buffer, refs []externalTypeRef)
func writeTypeScriptTypes(b *bytes.Buffer, types []typeDeclaration)
func writeTypeScriptClient(b *bytes.Buffer, procedures []procedure)
func writeTypeScriptTransportMethods(b *bytes.Buffer)
func writeTypeScriptProcedureType(b *bytes.Buffer, p procedure, typeName string)
type typeScriptNames struct {
	GroupMember     map[string]string
	GroupClass      map[string]string
	RootMember      map[string]string
	ProcedureMember map[string]string
	ProcedureClass  map[string]string
}
func newTypeScriptNames(procedures []procedure, groups map[string][]procedure) typeScriptNames
func uniqueTypeScriptName(base string, used map[string]int) string
type typeScriptField struct {
	Name     string
	Type     string
	Optional bool
}
func typeScriptStructFields(structType *ast.StructType) []typeScriptField
func jsonFieldTag(field *ast.Field) (name string, optional bool, stringEncoded bool, skip bool)
func goExprToTypeScript(expr ast.Expr) string
func goIdentToTypeScript(name string) string
func goSelectorToTypeScript(expr *ast.SelectorExpr) string
func knownSelectorTypeScript(expr *ast.SelectorExpr) (string, bool)
func isKnownSelectorType(expr *ast.SelectorExpr) bool
func typeScriptExternalTypeName(expr *ast.SelectorExpr) string
func typeScriptStructLiteral(structType *ast.StructType) string
func typeScriptArrayType(inner string) string
func typeScriptProcedureType(goType string) string
func typeScriptProcedureClassName(p procedure) string
func typeScriptClassName(s string) string
func typeScriptMemberName(s string) string
func typeScriptTypeIdentifier(name string) string
func typeScriptTypeParams(params []string) string
func typeScriptPropertyName(name string) string
func lowerFirst(s string) string
func isTypeScriptIdentifier(s string) bool
func isTypeScriptIdentifierStart(ch byte) bool
func isTypeScriptIdentifierPart(ch byte) bool
func isTypeScriptReservedWord(s string) bool
```

Expected imports for `generate_ts.go`:

```go
import (
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"reflect"
	"slices"
	"strconv"
	"strings"
)
```

- [ ] **Step 4: Keep `command.go` focused**

After the moves, `cmd/neo-gen/command.go` should contain only `package main` and any declarations that were intentionally not assigned to a new file. If it is empty except for `package main`, delete it. Do not move or alter `cmd/neo-gen/main.go`.

- [ ] **Step 5: Format and test generator package**

Run:

```bash
gofmt -w cmd/neo-gen/*.go
go test ./cmd/neo-gen
```

Expected: package builds and all generator tests pass.

---

### Task 4: Split Binary Codec Files

**Files:**
- Modify: `binary.go`
- Create: `binary_encode.go`
- Create: `binary_decode.go`
- Create: `binary_assign.go`
- Create: `binary_fields.go`
- Test: `binary_test.go`

- [ ] **Step 1: Keep public codec facade in `binary.go`**

After extraction, `binary.go` should contain:

```go
package neo

import (
	"bytes"
	"errors"
	"reflect"
)

const BinaryContentType = "application/x-neo-bin"

var binaryMagic = []byte{'N', 'E', 'O', '1'}

const (
	binaryKindNull byte = iota
	binaryKindFalse
	binaryKindTrue
	binaryKindInt
	binaryKindUint
	binaryKindFloat
	binaryKindString
	binaryKindBytes
	binaryKindList
	binaryKindObject
)

type BinaryCodec struct{}

var NeoBinaryCodec BinaryCodec

func (BinaryCodec) Marshal(value any) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(binaryMagic)
	if err := writeBinaryValue(&buf, reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (BinaryCodec) Unmarshal(raw []byte, value any) error {
	if len(raw) < len(binaryMagic) || !bytes.Equal(raw[:len(binaryMagic)], binaryMagic) {
		return errors.New("invalid neo binary message")
	}

	out := reflect.ValueOf(value)
	if !out.IsValid() || out.Kind() != reflect.Pointer || out.IsNil() {
		return errors.New("binary unmarshal target must be a non-nil pointer")
	}

	decoder := binaryValueDecoder{raw: raw[len(binaryMagic):]}
	decoded, err := decoder.readValue()
	if err != nil {
		return err
	}
	if decoder.off != len(decoder.raw) {
		return errors.New("trailing bytes in neo binary message")
	}

	return assignBinaryValue(out.Elem(), decoded)
}
```

Preserve the existing public comments on `BinaryContentType`, `BinaryCodec`, and `NeoBinaryCodec`.

- [ ] **Step 2: Move encoder into `binary_encode.go`**

Move these existing declarations without changing bodies:

```go
func writeBinaryValue(buf *bytes.Buffer, value reflect.Value) error
func writeBinaryStringValue(buf *bytes.Buffer, marshaler encoding.TextMarshaler) error
func writeBinaryBytesValue(buf *bytes.Buffer, marshaler encoding.BinaryMarshaler) error
func writeBinaryList(buf *bytes.Buffer, value reflect.Value) error
func writeBinaryMap(buf *bytes.Buffer, value reflect.Value) error
func writeBinaryStruct(buf *bytes.Buffer, value reflect.Value) error
func writeBinaryUvarint(buf *bytes.Buffer, value uint64)
func writeBinaryString(buf *bytes.Buffer, value string)
func writeBinaryBytes(buf *bytes.Buffer, value []byte)
func encodeZigZag(value int64) uint64
```

Expected imports:

```go
import (
	"bytes"
	"encoding"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"sort"
)
```

- [ ] **Step 3: Move decoder into `binary_decode.go`**

Move these existing declarations without changing bodies:

```go
func decodeZigZag(value uint64) int64
type binaryValueDecoder struct {
	raw []byte
	off int
}
func (decoder *binaryValueDecoder) readValue() (any, error)
func (decoder *binaryValueDecoder) readByte() (byte, error)
func (decoder *binaryValueDecoder) readUvarint() (uint64, error)
func (decoder *binaryValueDecoder) readBytes() ([]byte, error)
func (decoder *binaryValueDecoder) readBytesOfLen(size int) ([]byte, error)
```

Expected imports:

```go
import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)
```

- [ ] **Step 4: Move assignment into `binary_assign.go`**

Move these existing declarations without changing bodies:

```go
func assignBinaryValue(dst reflect.Value, src any) error
func assignBinaryStruct(dst reflect.Value, object binaryObject) error
func assignBinaryScalar(dst reflect.Value, src any) error
func assignBinaryTextValue(dst reflect.Value, src any) (bool, error)
func assignBinaryBytesValue(dst reflect.Value, src any) (bool, error)
func assignBinaryMapKey(dst reflect.Value, key string) error
func binaryInt64(src any) (int64, error)
func binaryUint64(src any) (uint64, error)
func binaryFloat64(src any) (float64, error)
```

Expected imports:

```go
import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
)
```

- [ ] **Step 5: Move field/tag helpers into `binary_fields.go`**

Move these existing declarations without changing bodies:

```go
type binaryObject []binaryField

type binaryField struct {
	number uint64
	name   string
	value  any
}

type binaryStructField struct {
	index         []int
	name          string
	number        uint64
	omitEmpty     bool
	stringEncoded bool
}
func binaryStructFields(t reflect.Type) []binaryStructField
func parseJSONTag(tag string) (string, map[string]bool)
func binaryMapKey(value reflect.Value) string
func stringTaggedValue(value reflect.Value) (string, error)
func isBinaryEmptyValue(value reflect.Value) bool
func binaryToAny(src any) any
```

Expected imports:

```go
import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)
```

- [ ] **Step 6: Format and test root package**

Run:

```bash
gofmt -w binary.go binary_encode.go binary_decode.go binary_assign.go binary_fields.go
go test ./...
```

Expected: root module packages build and all tests pass.

---

### Task 5: Final Verification

**Files:**
- Read: changed Go files
- Read: `docs/superpowers/specs/2026-06-07-codebase-refactor-design.md`

- [ ] **Step 1: Verify formatting**

Run:

```bash
test -z "$(gofmt -l .)"
```

Expected: exit 0. If it reports files, run `gofmt -w` on those files and repeat.

- [ ] **Step 2: Verify root tests**

Run:

```bash
go test ./...
```

Expected: all root packages pass.

- [ ] **Step 3: Verify examples module**

Run:

```bash
go test ./...
```

Working directory: `examples`

Expected: examples compile.

- [ ] **Step 4: Verify benchmarks module**

Run:

```bash
go test ./...
```

Working directory: `benchmarks`

Expected: benchmarks compile.

- [ ] **Step 5: Verify vet**

Run:

```bash
go vet ./...
```

Expected: no vet findings.

- [ ] **Step 6: Verify race tests**

Run:

```bash
go test -race ./...
```

Expected: all root module race tests pass.

- [ ] **Step 7: Review diff**

Run:

```bash
git diff --stat
git diff --name-only
```

Expected: diff is limited to the plan and refactor files. Generated example artifacts are not manually edited.
