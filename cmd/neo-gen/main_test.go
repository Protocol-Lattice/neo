package main

import (
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

	got := inferPrefixes(receivers, nested)
	want := map[string]string{
		"root":  "",
		"admin": "api",
		"user":  "api.user",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prefixes = %#v, want %#v", got, want)
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

	src, err := generateTypeScript(scan.Procedures, scan.Types)
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
	assertContains(t, text, "export class TypedClient {")
	assertContains(t, text, "readonly user: UserClient;")
	assertContains(t, text, "readonly health: HealthProcedure;")
	assertContains(t, text, "export function createClient(addr: string, options?: NeoClientOptions): TypedClient")
	assertContains(t, text, `return this.client.request<NoInput, User>("GET", "health", input, options);`)
	assertContains(t, text, `return this.client.request<Record<string, never>, User[]>("GET", "inline", input, options);`)
	assertContains(t, text, `return this.client.request<CreateInput, User>("POST", "user.create", input, options);`)
	assertContains(t, text, `return this.client.subscribe<NoInput, User>("user.changes", input, options);`)
	assertContains(t, text, `return this.client.subscribeWebSocket<NoInput, User>("user.changes", input, options);`)
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
