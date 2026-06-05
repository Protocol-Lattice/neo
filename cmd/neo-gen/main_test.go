package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScanDirFindsRootNestedAndSubscriptionProcedures(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.go", `package example

type ListInput struct{ Limit int }
type User struct{ Name string }
type CreateInput struct{ Name string }
type EventInput struct{ ID string }
type Event struct{ Name string }

func register(root, user, events Router) {
	root.Register("health", neo.Query[struct{}, string]())
	root.Nested("user", user)
	user.Register("list", neo.Query[ListInput, []User]())
	user.Register("create", neo.Mutation[CreateInput, User]())
	root.Nested("events", events)
	events.RegisterSubscription("updates", neo.Subscription[EventInput, Event]())
}
`)
	writeFile(t, dir, "ignored_test.go", `package example
func ignored(r Router) { r.Register("ignored", neo.Query[int, int]()) }
`)
	writeFile(t, dir, "ignored.gen.go", `package example
func ignoredGenerated(r Router) { r.Register("ignoredGenerated", neo.Query[int, int]()) }
`)

	pkg, procedures, err := scanDir(dir)
	if err != nil {
		t.Fatalf("scanDir returned error: %v", err)
	}
	if pkg != "example" {
		t.Fatalf("package name = %q, want example", pkg)
	}

	got := make([]procedure, len(procedures))
	copy(got, procedures)
	want := []procedure{
		{Receiver: "events", Key: "events.updates", Kind: "subscription", Input: "EventInput", Output: "Event"},
		{Receiver: "root", Key: "health", Kind: "query", Input: "struct{}", Output: "string"},
		{Receiver: "user", Key: "user.create", Kind: "mutation", Input: "CreateInput", Output: "User"},
		{Receiver: "user", Key: "user.list", Kind: "query", Input: "ListInput", Output: "[]User"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("procedures mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestScanDirReturnsHelpfulErrors(t *testing.T) {
	t.Run("missing package", func(t *testing.T) {
		dir := t.TempDir()
		_, _, err := scanDir(dir)
		if err == nil || !strings.Contains(err.Error(), "no Go package found") {
			t.Fatalf("error = %v, want no Go package found", err)
		}
	})

	t.Run("syntax error", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "broken.go", `package example
func broken( {`)
		_, _, err := scanDir(dir)
		if err == nil {
			t.Fatal("expected parse error, got nil")
		}
	})
}

func TestRunNeoGenWritesGeneratedFileAndStatus(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.go", `package example

type Input struct{ Name string }
type Output struct{ Message string }

func register(root Router) {
	root.Register("hello", neo.Query[Input, Output]())
}
`)

	out := filepath.Join(t.TempDir(), "nested", "neo.gen.go")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runNeoGen([]string{"-dir", dir, "-out", out}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runNeoGen returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	text := string(raw)
	assertContains(t, text, "package example")
	assertContains(t, text, "type TypedClient struct {")
	assertContains(t, text, "func (p HelloProcedure) Query(ctx context.Context, input Input) (Output, error)")
	assertContains(t, stdout.String(), "neo-gen: generated 1 typed procedures in "+out)
}

func TestRunNeoGenReturnsCommandErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.go", `package example
func register(root Router) {}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runNeoGen([]string{"-dir", dir, "-target", "bogus"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no typed procedures found") {
		t.Fatalf("error = %v, want no typed procedures found", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestGenerateTargetRejectsUnsupportedTarget(t *testing.T) {
	_, err := generateTarget(packageScan{
		Package: "example",
		Procedures: []procedure{
			{Key: "hello", Kind: "query", Input: "struct{}", Output: "string"},
		},
	}, "example", commandConfig{
		dir:    ".",
		target: "bogus",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported target") {
		t.Fatalf("error = %v, want unsupported target", err)
	}
}

func TestRegisterCall(t *testing.T) {
	expr := parseCall(t, `router.Register("user.get-by_id", neo.Query[GetUserInput, *User]())`)
	receiver, key, kind, input, output, ok := registerCall(expr)
	if !ok {
		t.Fatal("registerCall did not recognize typed registration")
	}
	assertEqual(t, receiver, "router")
	assertEqual(t, key, "user.get-by_id")
	assertEqual(t, kind, "query")
	assertEqual(t, input, "GetUserInput")
	assertEqual(t, output, "*User")
}

func TestStringLiteralUnquotesEscapes(t *testing.T) {
	expr, err := parser.ParseExpr(`"user.\u0067et"`)
	if err != nil {
		t.Fatalf("parse expr: %v", err)
	}

	got, ok := stringLiteral(expr)
	if !ok {
		t.Fatal("stringLiteral ok = false, want true")
	}
	assertEqual(t, got, "user.get")
}

func TestRegisterCallRejectsUnsupportedShapes(t *testing.T) {
	tests := []string{
		`router.Handle("x", neo.Query[In, Out]())`,
		`router.Register(dynamicKey, neo.Query[In, Out]())`,
		`router.Register("x", neo.Query[In]())`,
		`router.Register("x", neo.Command[In, Out]())`,
		`router.Register("x", Query[In, Out])`,
	}

	for _, tc := range tests {
		t.Run(tc, func(t *testing.T) {
			if _, _, _, _, _, ok := registerCall(parseCall(t, tc)); ok {
				t.Fatalf("registerCall(%s) ok = true, want false", tc)
			}
		})
	}
}

func TestNestedCall(t *testing.T) {
	parent, prefix, child, ok := nestedCall(parseCall(t, `root.Nested("admin.v1", adminRouter)`))
	if !ok {
		t.Fatal("nestedCall did not recognize nested router")
	}
	assertEqual(t, parent, "root")
	assertEqual(t, prefix, "admin.v1")
	assertEqual(t, child, "adminRouter")
}

func TestInferPrefixesNestedChain(t *testing.T) {
	receivers := map[string]struct{}{
		"root":  {},
		"admin": {},
		"user":  {},
	}
	nested := []nestedRouter{
		{Parent: "root", Prefix: "api", Child: "admin"},
		{Parent: "admin", Prefix: "user", Child: "user"},
	}

	got, err := inferPrefixes(receivers, nested)
	if err != nil {
		t.Fatalf("inferPrefixes returned error: %v", err)
	}
	want := map[string]string{
		"root":  "",
		"admin": "api",
		"user":  "api.user",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prefixes = %#v, want %#v", got, want)
	}
}

func TestInferPrefixesRejectsInvalidNestedGraphs(t *testing.T) {
	tests := []struct {
		name      string
		receivers map[string]struct{}
		nested    []nestedRouter
		want      string
	}{
		{
			name: "cycle",
			receivers: map[string]struct{}{
				"root": {},
				"user": {},
			},
			nested: []nestedRouter{
				{Parent: "root", Prefix: "user", Child: "user"},
				{Parent: "user", Prefix: "root", Child: "root"},
			},
			want: "cycle",
		},
		{
			name: "conflicting prefixes",
			receivers: map[string]struct{}{
				"root":  {},
				"admin": {},
				"user":  {},
			},
			nested: []nestedRouter{
				{Parent: "root", Prefix: "public", Child: "user"},
				{Parent: "admin", Prefix: "private", Child: "user"},
			},
			want: "conflicting prefixes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := inferPrefixes(tt.receivers, tt.nested)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestKeyHelpers(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "full empty prefix", got: fullProcedureKey("", "health"), want: "health"},
		{name: "full trims dots", got: fullProcedureKey(".user.", ".list."), want: "user.list"},
		{name: "full does not duplicate", got: fullProcedureKey("user", "user.list"), want: "user.list"},
		{name: "join empty left", got: joinKey("", "user"), want: "user"},
		{name: "join empty right", got: joinKey("user", ""), want: "user"},
		{name: "join trims", got: joinKey(".api.", ".user."), want: "api.user"},
		{name: "last part", got: lastKeyPart("api.user.list"), want: "list"},
		{name: "export separators", got: exportName("user-get/by_id"), want: "UserGetById"},
		{name: "export empty", got: exportName("---"), want: "Procedure"},
		{name: "proc type", got: procTypeName(procedure{Key: "api.user.list"}), want: "ApiUserList"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestTypedProcedureSupportsSelectorAndLocalGenericCalls(t *testing.T) {
	tests := []struct {
		call string
		kind string
		in   string
		out  string
	}{
		{call: `neo.Query[map[string]int, []User]()`, kind: "query", in: "map[string]int", out: "[]User"},
		{call: `Mutation[*CreateInput, User]()`, kind: "mutation", in: "*CreateInput", out: "User"},
		{call: `neo.Subscription[EventInput, chan Event]()`, kind: "subscription", in: "EventInput", out: "chan Event"},
	}

	for _, tt := range tests {
		t.Run(tt.call, func(t *testing.T) {
			kind, in, out, ok := typedProcedure(parseCall(t, tt.call))
			if !ok {
				t.Fatalf("typedProcedure(%s) ok = false", tt.call)
			}
			assertEqual(t, kind, tt.kind)
			assertEqual(t, in, tt.in)
			assertEqual(t, out, tt.out)
		})
	}
}

func TestGenerateProducesTypedClientForGroupedRootAndSubscriptionProcedures(t *testing.T) {
	procedures := []procedure{
		{Key: "health", Kind: "query", Input: "struct{}", Output: "string"},
		{Key: "user.list", Kind: "query", Input: "ListInput", Output: "[]User"},
		{Key: "user.create", Kind: "mutation", Input: "CreateInput", Output: "User"},
		{Key: "events.updates", Kind: "subscription", Input: "EventInput", Output: "Event"},
	}

	src, err := generate("example", procedures)
	if err != nil {
		t.Fatalf("generate returned error: %v\n%s", err, src)
	}
	text := string(src)

	assertContains(t, text, "package example")
	assertContains(t, text, "type TypedClient struct {")
	assertContains(t, text, "Health HealthProcedure")
	assertContains(t, text, "User   UserClient")
	assertContains(t, text, "Events EventsClient")
	assertContains(t, text, "tc.User.List = UserListProcedure{client: c}")
	assertContains(t, text, "tc.User.Create = UserCreateProcedure{client: c}")
	assertContains(t, text, "tc.Events.Updates = EventsUpdatesProcedure{client: c}")
	assertContains(t, text, "func NewTypedClient(addr string, opts ...neo.ClientOption) *TypedClient")
	assertContains(t, text, "func NewTypedClientFromClient(c *neo.Client) *TypedClient")
	assertContains(t, text, "func (p HealthProcedure) Query(ctx context.Context, input struct{}) (string, error)")
	assertContains(t, text, "func (p HealthProcedure) Call(ctx context.Context, input struct{}) (string, error)")
	assertContains(t, text, `neo.CallTyped[struct{}, string](ctx, p.client.Query.Procedure("health"), input)`)
	assertContains(t, text, "func (p UserCreateProcedure) Mutate(ctx context.Context, input CreateInput) (User, error)")
	assertContains(t, text, "func (p UserCreateProcedure) Call(ctx context.Context, input CreateInput) (User, error)")
	assertContains(t, text, `neo.CallTyped[CreateInput, User](ctx, p.client.Mutation.Procedure("user.create"), input)`)
	assertContains(t, text, "func (p EventsUpdatesProcedure) Subscribe(ctx context.Context, input EventInput) (<-chan Event, error)")
	assertContains(t, text, `neo.SubscribeTyped[EventInput, Event](ctx, p.client.Subscription.Procedure("events.updates"), input)`)
}

func TestGenerateTypeScriptProducesTypedClientAndLocalTypes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.go", `package example

type NoInput struct{}
type CreateInput struct {
	Name      string          `+"`json:\"name\"`"+`
	Age       int             `+"`json:\"age,omitempty\"`"+`
	CreatedAt time.Time       `+"`json:\"createdAt\"`"+`
	Tags      []string        `+"`json:\"tags\"`"+`
	Maybe     *User           `+"`json:\"maybe,omitempty\"`"+`
	Raw       json.RawMessage `+"`json:\"raw,omitempty\"`"+`
	Secret    string          `+"`json:\"-\"`"+`
}
type User struct {
	ID         int            `+"`json:\"id\"`"+`
	Name       string         `+"`json:\"name\"`"+`
	Attributes map[string]any `+"`json:\"attributes\"`"+`
}

func register(root, user Router) {
	root.Register("health", neo.Query[NoInput, User]())
	root.Register("inline", neo.Query[struct{}, []User]())
	root.Nested("user", user)
	user.Register("create", neo.Mutation[CreateInput, User]())
	user.RegisterSubscription("changes", neo.Subscription[NoInput, User]())
}
`)

	scan, err := scanPackage(dir)
	if err != nil {
		t.Fatalf("scanPackage returned error: %v", err)
	}

	src, err := generateTypeScript(scan.Procedures, scan.Types, typeScriptOptions{
		RuntimeImport: "./neo.runtime.ts",
	})
	if err != nil {
		t.Fatalf("generateTypeScript returned error: %v", err)
	}
	text := string(src)

	assertContains(t, text, "export type NoInput = Record<string, never>;")
	assertContains(t, text, "export interface CreateInput {")
	assertContains(t, text, "name: string;")
	assertContains(t, text, "age?: number;")
	assertContains(t, text, "createdAt: string;")
	assertContains(t, text, "tags: string[];")
	assertContains(t, text, "maybe?: User | null;")
	assertContains(t, text, "raw?: unknown;")
	if strings.Contains(text, "secret") || strings.Contains(text, "Secret") {
		t.Fatalf("generated source should not include json-ignored field\n--- source ---\n%s", text)
	}
	assertContains(t, text, "attributes: Record<string, unknown>;")
	assertContains(t, text, `import { NeoClientCore, type NeoCallOptions, type NeoClientOptions } from "./neo.runtime.ts";`)
	assertContains(t, text, `export { NeoError } from "./neo.runtime.ts";`)
	assertContains(t, text, "export class TypedClient extends NeoClientCore {")
	assertContains(t, text, "readonly user: UserClient;")
	assertContains(t, text, "readonly health: HealthProcedure;")
	assertContains(t, text, "export function createClient(addr: string, options?: NeoClientOptions): TypedClient")
	assertContains(t, text, `return this.client.request<NoInput, User>("GET", "health", input, options);`)
	assertContains(t, text, `return this.client.request<Record<string, never>, User[]>("GET", "inline", input, options);`)
	assertContains(t, text, `return this.client.request<CreateInput, User>("POST", "user.create", input, options);`)
	assertContains(t, text, `return this.client.subscribe<NoInput, User>("user.changes", input, options);`)
	assertContains(t, text, `return this.client.subscribeWebSocket<NoInput, User>("user.changes", input, options);`)
}

func TestGenerateTypeScriptHandlesExternalTypesAndNameCollisions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.go", `package example

type ExternalInput struct {
	ID    uuid.UUID             `+"`json:\"id\"`"+`
	Page  domain.Page[api.User] `+"`json:\"page\"`"+`
	Owner api.User              `+"`json:\"owner\"`"+`
}
type User struct {
	Owner api.User `+"`json:\"owner\"`"+`
}

func register(root Router) {
	root.Register("user.get-id", neo.Query[ExternalInput, domain.Page[api.User]]())
	root.Register("user.get_id", neo.Query[ExternalInput, domain.Page[api.User]]())
}
`)

	scan, err := scanPackage(dir)
	if err != nil {
		t.Fatalf("scanPackage returned error: %v", err)
	}

	src, err := generateTypeScript(scan.Procedures, scan.Types, typeScriptOptions{
		RuntimeImport: "./neo.runtime.ts",
	})
	if err != nil {
		t.Fatalf("generateTypeScript returned error: %v", err)
	}
	text := string(src)

	assertContains(t, text, "export type ApiUser = unknown;")
	assertContains(t, text, "export type DomainPage<T1 = unknown> = unknown;")
	assertContains(t, text, "export type UuidUUID = unknown;")
	assertContains(t, text, "id: UuidUUID;")
	assertContains(t, text, "page: DomainPage<ApiUser>;")
	assertContains(t, text, "owner: ApiUser;")
	assertContains(t, text, "readonly getId: UserGetIdProcedure;")
	assertContains(t, text, "readonly getId2: UserGetIdProcedure2;")
	assertContains(t, text, `return this.client.request<ExternalInput, DomainPage<ApiUser>>("GET", "user.get-id", input, options);`)
	assertContains(t, text, `return this.client.request<ExternalInput, DomainPage<ApiUser>>("GET", "user.get_id", input, options);`)
}

func TestExamplesTypeScriptClientGeneratedFilesAreCurrent(t *testing.T) {
	exampleDir := filepath.Clean("../../examples/ts_client")

	scan, err := scanPackage(exampleDir)
	if err != nil {
		t.Fatalf("scanPackage examples/ts_client: %v", err)
	}

	runtime, err := generateTypeScriptRuntime()
	if err != nil {
		t.Fatalf("generateTypeScriptRuntime returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(exampleDir, "neo.runtime.ts"), string(runtime))

	client, err := generateTypeScript(scan.Procedures, scan.Types, typeScriptOptions{
		RuntimeImport: "./neo.runtime.ts",
	})
	if err != nil {
		t.Fatalf("generateTypeScript returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(exampleDir, "neo.gen.ts"), string(client))
}

func parseCall(t *testing.T, src string) *ast.CallExpr {
	t.Helper()
	expr, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("parse expr %q: %v", src, err)
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expr %q is %T, want *ast.CallExpr", src, expr)
	}
	return call
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("generated source missing %q\n--- source ---\n%s", want, text)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got := string(raw); got != want {
		t.Fatalf("%s is not current; regenerate it with neo-gen", path)
	}
}
