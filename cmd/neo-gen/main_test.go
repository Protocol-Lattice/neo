package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type openAPITestDocument struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Paths      map[string]map[string]openAPITestOperation `json:"paths"`
	Components struct {
		Schemas map[string]map[string]any `json:"schemas"`
	} `json:"components"`
}

type openAPITestOperation struct {
	OperationID string                       `json:"operationId"`
	Summary     string                       `json:"summary,omitempty"`
	Description string                       `json:"description,omitempty"`
	Tags        []string                     `json:"tags,omitempty"`
	Deprecated  bool                         `json:"deprecated,omitempty"`
	Parameters  []openAPITestParameter       `json:"parameters,omitempty"`
	RequestBody *openAPITestRequestBody      `json:"requestBody,omitempty"`
	Responses   map[string]openAPITestResult `json:"responses"`
}

type openAPITestParameter struct {
	Name    string                          `json:"name"`
	In      string                          `json:"in"`
	Content map[string]openAPITestMediaType `json:"content"`
}

type openAPITestRequestBody struct {
	Required bool                            `json:"required"`
	Content  map[string]openAPITestMediaType `json:"content"`
}

type openAPITestResult struct {
	Description string                          `json:"description"`
	Content     map[string]openAPITestMediaType `json:"content,omitempty"`
}

type openAPITestMediaType struct {
	Schema map[string]any `json:"schema"`
}

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

func TestScanDirFindsGatewayMountedRoutersAndProxyMetadata(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "gateway.go", `package example

type GetUserInput struct{ ID int }
type User struct{ Name string }
type CreateOrderInput struct{ UserID int }
type Order struct{ ID int }
type OrderEvent struct{ Name string }

func register(gateway Gateway, users Router, ordersURL string) {
	users.Register("getByID", neo.Query[GetUserInput, User]())
	gateway.Mount("users", users)
	gateway.Proxy("orders", ordersURL, neo.WithProxyMetadata(
		neo.ProcedureMeta{Key: "create", Kind: neo.ProcedureKindMutation, Input: "CreateOrderInput", Output: "Order"},
		neo.ProcedureMeta{Key: "changes", Kind: neo.ProcedureKindSubscription, Input: "struct{}", Output: "OrderEvent"},
	))
}
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
		{Receiver: "gateway", Key: "orders.changes", Kind: "subscription", Input: "struct{}", Output: "OrderEvent"},
		{Receiver: "gateway", Key: "orders.create", Kind: "mutation", Input: "CreateOrderInput", Output: "Order"},
		{Receiver: "users", Key: "users.getByID", Kind: "query", Input: "GetUserInput", Output: "User"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("procedures mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestScanDirFindsProcedureMetadataOptions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.go", `package example

type Input struct{}
type Output struct{}

func register(root Router) {
	root.Register("user.get", neo.Query[Input, Output](
		nil,
		neo.WithSummary("Get user"),
		neo.WithDescription("Returns a user by ID."),
		neo.WithTags("users", "read"),
		neo.WithDeprecated(),
	))
	root.Register("user.create", neo.Mutation[Input, Output](
		nil,
		neo.WithProcedureMeta(neo.ProcedureMeta{
			Summary: "Create user",
			Tags: []string{"users", "write"},
		}),
	))
}
`)

	scan, err := scanPackage(dir)
	if err != nil {
		t.Fatalf("scanPackage returned error: %v", err)
	}
	if len(scan.Procedures) != 2 {
		t.Fatalf("procedures len = %d, want 2", len(scan.Procedures))
	}

	var get procedure
	for _, procedure := range scan.Procedures {
		if procedure.Key == "user.get" {
			get = procedure
			break
		}
	}
	if get.Key != "user.get" {
		t.Fatalf("procedure key = %q, want user.get", get.Key)
	}
	if get.Summary != "Get user" || get.Description != "Returns a user by ID." {
		t.Fatalf("procedure metadata = %#v", get)
	}
	if strings.Join(get.Tags, ",") != "users,read" {
		t.Fatalf("tags = %#v", get.Tags)
	}
	if !get.Deprecated {
		t.Fatal("deprecated = false, want true")
	}
}

func TestScanDirReturnsHelpfulErrors(t *testing.T) {
	t.Run("missing package", func(t *testing.T) {
		dir := t.TempDir()
		_, _, err := scanDir(dir)
		if err == nil || !strings.Contains(err.Error(), "no go package found") {
			t.Fatalf("error = %v, want no go package found", err)
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

func TestRunNeoGenWritesGeneratedFileFromMetadataURL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "types.go", `package example

type CreateInput struct{ Name string }
type User struct{ Name string }
`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/neo/_meta" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]remoteProcedureMeta{
			{Key: "user.create", Kind: "mutation", Input: "CreateInput", Output: "User"},
		})
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "neo.gen.go")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runNeoGen([]string{"-dir", dir, "-metadata-url", server.URL + "/neo", "-out", out}, &stdout, &stderr)
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
	assertContains(t, text, "func (p UserCreateProcedure) Mutate(ctx context.Context, input CreateInput) (User, error)")
	assertContains(t, text, `neo.CallTyped[CreateInput, User](ctx, p.client.Mutation.Procedure("user.create"), input)`)
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

func TestRouterPrefixCallRecognizesGatewayMount(t *testing.T) {
	parent, prefix, child, ok := routerPrefixCall(parseCall(t, `gateway.Mount("users", usersRouter)`))
	if !ok {
		t.Fatal("routerPrefixCall did not recognize gateway mount")
	}
	assertEqual(t, parent, "gateway")
	assertEqual(t, prefix, "users")
	assertEqual(t, child, "usersRouter")
}

func TestGatewayProxyCallFindsProcedureMetadata(t *testing.T) {
	receiver, procedures, ok := gatewayProxyCall(parseCall(t, `gateway.Proxy("orders", ordersURL, neo.WithProxyMetadata(
		neo.ProcedureMeta{Key: "create", Kind: neo.ProcedureKindMutation, Input: "CreateOrderInput", Output: "Order"},
		neo.ProcedureMeta{Key: "list", Kind: "query", Input: "struct{}", Output: "[]Order"},
	))`))
	if !ok {
		t.Fatal("gatewayProxyCall did not recognize proxy metadata")
	}
	assertEqual(t, receiver, "gateway")

	want := []procedure{
		{Receiver: "gateway", Key: "orders.create", Kind: "mutation", Input: "CreateOrderInput", Output: "Order"},
		{Receiver: "gateway", Key: "orders.list", Kind: "query", Input: "struct{}", Output: "[]Order"},
	}
	if !reflect.DeepEqual(procedures, want) {
		t.Fatalf("procedures = %#v, want %#v", procedures, want)
	}
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
	assertContains(t, text, "func (tc *TypedClient) Metadata(ctx context.Context) ([]neo.ProcedureMeta, error)")
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

func TestGenerateDocsAndSchemaUseProcedureMetadata(t *testing.T) {
	procedures := []procedure{
		{
			Key:         "user.get",
			Kind:        "query",
			Input:       "GetUserInput",
			Output:      "User",
			Summary:     "Get user",
			Description: "Returns a user by ID.",
			Tags:        []string{"users", "read"},
			Deprecated:  true,
		},
	}

	docs, err := generateDocs(procedures)
	if err != nil {
		t.Fatalf("generateDocs returned error: %v", err)
	}
	docText := string(docs)
	assertContains(t, docText, "# Neo API")
	assertContains(t, docText, "## `user.get`")
	assertContains(t, docText, "Get user")
	assertContains(t, docText, "> Deprecated.")
	assertContains(t, docText, "- Tags: `users`, `read`")

	schema, err := generateSchema(procedures)
	if err != nil {
		t.Fatalf("generateSchema returned error: %v", err)
	}
	schemaText := string(schema)
	assertContains(t, schemaText, `"schema": "https://protocol-lattice.github.io/neo/schema/v1"`)
	assertContains(t, schemaText, `"key": "user.get"`)
	assertContains(t, schemaText, `"deprecated": true`)
}

func TestGenerateOpenAPIMapsNeoProcedures(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "routes.go", `package example

type NoInput struct{}
type GetUserInput struct {
	ID           int  `+"`json:\"id\"`"+`
	IncludePosts bool `+"`json:\"includePosts,omitempty\"`"+`
}
type User struct {
	ID   int      `+"`json:\"id\"`"+`
	Name string   `+"`json:\"name\"`"+`
	Tags []string `+"`json:\"tags,omitempty\"`"+`
}
type UserEvent struct {
	Name string `+"`json:\"name\"`"+`
	Data User   `+"`json:\"data\"`"+`
}

func register(root, users Router) {
	root.Register("health", neo.Query[NoInput, User](nil))
	root.Nested("user", users)
	users.Register("getByID", neo.Query[GetUserInput, User](
		nil,
		neo.WithSummary("Get user"),
		neo.WithDescription("Returns one user."),
		neo.WithTags("users", "read"),
	))
	users.Register("create", neo.Mutation[GetUserInput, User](
		nil,
		neo.WithDeprecated(),
	))
	users.RegisterSubscription("changes", neo.Subscription[NoInput, UserEvent](nil))
}
`)

	scan, err := scanPackage(dir)
	if err != nil {
		t.Fatalf("scanPackage returned error: %v", err)
	}

	src, err := generateOpenAPI(scan.Procedures, scan.Types)
	if err != nil {
		t.Fatalf("generateOpenAPI returned error: %v", err)
	}
	doc := decodeOpenAPIDocument(t, src)

	assertEqual(t, doc.OpenAPI, "3.1.0")
	assertEqual(t, doc.Info.Title, "Neo API")
	assertEqual(t, doc.Info.Version, "1.0.0")

	get := doc.Paths["/user.getByID"]["get"]
	assertEqual(t, get.OperationID, "user_getByID_query")
	assertEqual(t, get.Summary, "Get user")
	assertEqual(t, get.Description, "Returns one user.")
	if !reflect.DeepEqual(get.Tags, []string{"users", "read"}) {
		t.Fatalf("GET tags = %#v, want users/read", get.Tags)
	}
	if len(get.Parameters) != 1 {
		t.Fatalf("GET parameters len = %d, want 1", len(get.Parameters))
	}
	assertEqual(t, get.Parameters[0].Name, "input")
	assertEqual(t, get.Parameters[0].In, "query")
	assertSchemaRef(t, get.Parameters[0].Content["application/json"].Schema, "#/components/schemas/GetUserInput")

	queryPost := doc.Paths["/user.getByID"]["post"]
	assertEqual(t, queryPost.OperationID, "user_getByID_query_post")
	assertRequestEnvelopeInputRef(t, queryPost.RequestBody, "#/components/schemas/GetUserInput")
	assertResponseEnvelopeResultRef(t, queryPost.Responses["200"], "#/components/schemas/User")

	mutation := doc.Paths["/user.create"]["post"]
	assertEqual(t, mutation.OperationID, "user_create_mutation")
	if !mutation.Deprecated {
		t.Fatal("mutation deprecated = false, want true")
	}
	assertRequestEnvelopeInputRef(t, mutation.RequestBody, "#/components/schemas/GetUserInput")
	assertResponseEnvelopeResultRef(t, mutation.Responses["200"], "#/components/schemas/User")

	subscription := doc.Paths["/user.changes"]["get"]
	assertEqual(t, subscription.OperationID, "user_changes_subscription")
	assertSchemaRef(t, subscription.Parameters[0].Content["application/json"].Schema, "#/components/schemas/NoInput")
	assertResponseEnvelopeResultRefForContent(
		t,
		subscription.Responses["200"],
		"application/x-ndjson",
		"#/components/schemas/UserEvent",
	)

	getUserInput := doc.Components.Schemas["GetUserInput"]
	id := schemaProperty(t, getUserInput, "id")
	assertEqual(t, id["type"].(string), "integer")
	includePosts := schemaProperty(t, getUserInput, "includePosts")
	assertEqual(t, includePosts["type"].(string), "boolean")
	if got := schemaRequired(t, getUserInput); !reflect.DeepEqual(got, []string{"id"}) {
		t.Fatalf("GetUserInput required = %#v, want id only", got)
	}

	user := doc.Components.Schemas["User"]
	assertEqual(t, schemaProperty(t, user, "name")["type"].(string), "string")
	tags := schemaProperty(t, user, "tags")
	assertEqual(t, tags["type"].(string), "array")
	assertEqual(t, tags["items"].(map[string]any)["type"].(string), "string")
}

func TestRunNeoGenWritesOpenAPIFromMetadataURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/neo/_meta" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]remoteProcedureMeta{
			{
				Key:         "user.create",
				Kind:        "mutation",
				Input:       "CreateInput",
				Output:      "User",
				Summary:     "Create user",
				Description: "Creates one user.",
				Tags:        []string{"users"},
			},
		})
	}))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "openapi.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runNeoGen([]string{
		"-dir", t.TempDir(),
		"-metadata-url", server.URL + "/neo",
		"-target", "openapi",
		"-out", out,
	}, &stdout, &stderr)
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
	doc := decodeOpenAPIDocument(t, raw)

	mutation := doc.Paths["/user.create"]["post"]
	assertEqual(t, mutation.OperationID, "user_create_mutation")
	assertEqual(t, mutation.Summary, "Create user")
	assertEqual(t, mutation.Description, "Creates one user.")
	if !reflect.DeepEqual(mutation.Tags, []string{"users"}) {
		t.Fatalf("mutation tags = %#v, want users", mutation.Tags)
	}
	assertRequestEnvelopeInputRef(t, mutation.RequestBody, "#/components/schemas/CreateInput")
	assertResponseEnvelopeResultRef(t, mutation.Responses["200"], "#/components/schemas/User")
	if _, ok := doc.Components.Schemas["CreateInput"]; !ok {
		t.Fatal("components missing CreateInput metadata-only schema")
	}
	if _, ok := doc.Components.Schemas["User"]; !ok {
		t.Fatal("components missing User metadata-only schema")
	}
	assertContains(t, stdout.String(), "neo-gen: generated 1 typed procedures in "+out)
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

func TestGenerateTypeScriptDeclaresMetadataOnlyTypes(t *testing.T) {
	procedures := []procedure{
		{Key: "user.list", Kind: "query", Input: "ListInput", Output: "Page[User]"},
	}

	src, err := generateTypeScript(procedures, nil, typeScriptOptions{
		RuntimeImport: "./neo.runtime.ts",
	})
	if err != nil {
		t.Fatalf("generateTypeScript returned error: %v", err)
	}
	text := string(src)

	assertContains(t, text, "export type ListInput = unknown;")
	assertContains(t, text, "export type Page<T1 = unknown> = unknown;")
	assertContains(t, text, "export type User = unknown;")
	assertContains(t, text, `return this.client.request<ListInput, Page<User>>("GET", "user.list", input, options);`)
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
	assertContains(t, string(runtime), "async metadata(options: NeoCallOptions = {}): Promise<NeoProcedureMeta[]>")
	assertContains(t, string(runtime), "export type NeoProcedureMeta = {")
	assertFileContent(t, filepath.Join(exampleDir, "neo.runtime.ts"), string(runtime))

	client, err := generateTypeScript(scan.Procedures, scan.Types, typeScriptOptions{
		RuntimeImport: "./neo.runtime.ts",
	})
	if err != nil {
		t.Fatalf("generateTypeScript returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(exampleDir, "neo.gen.ts"), string(client))
}

func TestExamplesMicroservicesGatewayClientGeneratedFileIsCurrent(t *testing.T) {
	gatewayDir := filepath.Clean("../../examples/microservices/gateway")
	clientDir := filepath.Clean("../../examples/microservices/client")

	scan, err := scanPackage(gatewayDir)
	if err != nil {
		t.Fatalf("scanPackage examples/microservices/gateway: %v", err)
	}

	client, err := generate("main", scan.Procedures)
	if err != nil {
		t.Fatalf("generate gateway client returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(clientDir, "neo.gen.go"), string(client))
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

func decodeOpenAPIDocument(t *testing.T, src []byte) openAPITestDocument {
	t.Helper()

	var doc openAPITestDocument
	if err := json.Unmarshal(src, &doc); err != nil {
		t.Fatalf("decode OpenAPI document: %v\n%s", err, src)
	}
	return doc
}

func assertSchemaRef(t *testing.T, schema map[string]any, want string) {
	t.Helper()

	got, ok := schema["$ref"].(string)
	if !ok || got != want {
		t.Fatalf("schema ref = %#v, want %q", schema, want)
	}
}

func assertRequestEnvelopeInputRef(t *testing.T, body *openAPITestRequestBody, want string) {
	t.Helper()

	if body == nil {
		t.Fatal("requestBody = nil")
	}
	if !body.Required {
		t.Fatal("requestBody required = false, want true")
	}
	media := body.Content["application/json"]
	input := schemaProperty(t, media.Schema, "input")
	assertSchemaRef(t, input, want)
}

func assertResponseEnvelopeResultRef(t *testing.T, response openAPITestResult, want string) {
	t.Helper()

	assertResponseEnvelopeResultRefForContent(t, response, "application/json", want)
}

func assertResponseEnvelopeResultRefForContent(
	t *testing.T,
	response openAPITestResult,
	contentType string,
	want string,
) {
	t.Helper()

	media, ok := response.Content[contentType]
	if !ok {
		t.Fatalf("response missing %s content", contentType)
	}
	result := schemaProperty(t, media.Schema, "result")
	assertSchemaRef(t, result, want)
}

func schemaProperty(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", schema["properties"])
	}
	property, ok := properties[name].(map[string]any)
	if !ok {
		t.Fatalf("schema property %q = %#v", name, properties[name])
	}
	return property
}

func schemaRequired(t *testing.T, schema map[string]any) []string {
	t.Helper()

	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	required := make([]string, 0, len(raw))
	for _, value := range raw {
		item, ok := value.(string)
		if !ok {
			t.Fatalf("required value = %#v, want string", value)
		}
		required = append(required, item)
	}
	return required
}
