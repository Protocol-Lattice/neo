package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type procedure struct {
	Receiver string
	Key      string
	Kind     string
	Input    string
	Output   string
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

func main() {
	dir := flag.String("dir", ".", "directory containing procedure registrations")
	out := flag.String("out", "neo.gen.go", "output file")
	pkgOverride := flag.String("package", "", "optional generated package name")
	target := flag.String("target", "go", "generation target: go, ts, ts-runtime, or ts-standalone")
	tsRuntimeImport := flag.String("ts-runtime-import", "./neo.runtime.ts", "runtime import path for generated TypeScript clients")
	flag.Parse()

	scan, err := scanPackage(*dir)
	if err != nil {
		log.Fatalf("neo-gen: %v", err)
	}
	pkg := scan.Package
	if *pkgOverride != "" {
		pkg = *pkgOverride
	}
	if pkg == "" && *target == "go" {
		log.Fatalf("neo-gen: could not determine package for %s", *dir)
	}
	if len(scan.Procedures) == 0 {
		log.Fatalf("neo-gen: no typed procedures found in %s; expected neo.Query[In, Out](...), neo.Mutation[In, Out](...), or neo.Subscription[In, Out](...) inside Register/RegisterSubscription", *dir)
	}

	var src []byte
	switch strings.ToLower(*target) {
	case "go":
		src, err = generate(pkg, scan.Procedures)
	case "ts", "typescript":
		src, err = generateTypeScript(scan.Procedures, scan.Types, typeScriptOptions{
			RuntimeImport: *tsRuntimeImport,
		})
	case "ts-runtime", "typescript-runtime":
		src, err = generateTypeScriptRuntime()
	case "ts-standalone", "typescript-standalone":
		src, err = generateTypeScript(scan.Procedures, scan.Types, typeScriptOptions{
			Standalone: true,
		})
	default:
		log.Fatalf("neo-gen: unsupported target %q; expected go, ts, ts-runtime, or ts-standalone", *target)
	}
	if err != nil {
		log.Fatalf("neo-gen: generate: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("neo-gen: create output dir: %v", err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		log.Fatalf("neo-gen: write %s: %v", *out, err)
	}

	fmt.Printf("neo-gen: generated %d typed procedures in %s\n", len(scan.Procedures), *out)
}

func scanDir(dir string) (string, []procedure, error) {
	scan, err := scanPackage(dir)
	return scan.Package, scan.Procedures, err
}

func scanPackage(dir string) (packageScan, error) {
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, dir, func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, ".gen.go")
	}, parser.ParseComments)
	if err != nil {
		return packageScan{}, err
	}
	if len(packages) == 0 {
		return packageScan{}, fmt.Errorf("no Go package found in %s", dir)
	}

	var pkgName string
	var files []*ast.File
	for name, pkg := range packages {
		if strings.HasSuffix(name, "_test") {
			continue
		}
		pkgName = name
		for _, file := range pkg.Files {
			files = append(files, file)
		}
		break
	}

	var procedures []procedure
	var types []typeDeclaration
	var nested []nestedRouter
	seenReceivers := make(map[string]struct{})

	for _, file := range files {
		types = append(types, typeDeclarations(file)...)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			if parent, prefix, child, ok := nestedCall(call); ok {
				nested = append(nested, nestedRouter{Parent: parent, Prefix: prefix, Child: child})
				seenReceivers[parent] = struct{}{}
				seenReceivers[child] = struct{}{}
				return true
			}

			receiver, key, kind, in, out, ok := registerCall(call)
			if !ok {
				return true
			}

			seenReceivers[receiver] = struct{}{}
			procedures = append(procedures, procedure{
				Receiver: receiver,
				Key:      key,
				Kind:     kind,
				Input:    in,
				Output:   out,
			})

			return true
		})
	}

	prefixes := inferPrefixes(seenReceivers, nested)
	for i := range procedures {
		procedures[i].Key = fullProcedureKey(prefixes[procedures[i].Receiver], procedures[i].Key)
	}

	sort.Slice(procedures, func(i, j int) bool {
		return procedures[i].Key < procedures[j].Key
	})
	sort.Slice(types, func(i, j int) bool {
		return types[i].Name < types[j].Name
	})

	return packageScan{
		Package:    pkgName,
		Procedures: procedures,
		Types:      types,
	}, nil
}

func typeDeclarations(file *ast.File) []typeDeclaration {
	var types []typeDeclaration
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}

		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name == nil || typeSpec.Type == nil {
				continue
			}

			types = append(types, typeDeclaration{
				Name:       typeSpec.Name.Name,
				Type:       typeSpec.Type,
				TypeParams: typeParamNames(typeSpec.TypeParams),
			})
		}
	}

	return types
}

func typeParamNames(params *ast.FieldList) []string {
	if params == nil {
		return nil
	}

	var names []string
	for _, field := range params.List {
		for _, name := range field.Names {
			if name != nil && name.Name != "" {
				names = append(names, name.Name)
			}
		}
	}

	return names
}

func registerCall(call *ast.CallExpr) (receiver, key, kind, input, output string, ok bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Register" && sel.Sel.Name != "RegisterSubscription") || len(call.Args) < 2 {
		return "", "", "", "", "", false
	}

	receiver, ok = selectorReceiverName(sel.X)
	if !ok {
		return "", "", "", "", "", false
	}

	key, ok = stringLiteral(call.Args[0])
	if !ok || key == "" {
		return "", "", "", "", "", false
	}

	kind, input, output, ok = typedProcedure(call.Args[1])
	if !ok {
		return "", "", "", "", "", false
	}

	return receiver, key, kind, input, output, true
}

func nestedCall(call *ast.CallExpr) (parent, prefix, child string, ok bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Nested" || len(call.Args) < 2 {
		return "", "", "", false
	}

	parent, ok = selectorReceiverName(sel.X)
	if !ok {
		return "", "", "", false
	}

	prefix, ok = stringLiteral(call.Args[0])
	if !ok || prefix == "" {
		return "", "", "", false
	}

	childIdent, ok := call.Args[1].(*ast.Ident)
	if !ok || childIdent.Name == "" {
		return "", "", "", false
	}

	return parent, prefix, childIdent.Name, true
}

func inferPrefixes(receivers map[string]struct{}, nested []nestedRouter) map[string]string {
	prefixes := make(map[string]string)
	for receiver := range receivers {
		prefixes[receiver] = ""
	}

	changed := true
	for changed {
		changed = false
		for _, n := range nested {
			parentPrefix := prefixes[n.Parent]
			childPrefix := joinKey(parentPrefix, n.Prefix)
			if prefixes[n.Child] != childPrefix {
				prefixes[n.Child] = childPrefix
				changed = true
			}
		}
	}

	return prefixes
}

func fullProcedureKey(prefix, key string) string {
	prefix = strings.Trim(prefix, ".")
	key = strings.Trim(key, ".")
	if prefix == "" {
		return key
	}
	if key == prefix || strings.HasPrefix(key, prefix+".") {
		return key
	}
	return prefix + "." + key
}

func joinKey(left, right string) string {
	left = strings.Trim(left, ".")
	right = strings.Trim(right, ".")
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + "." + right
}

func typedProcedure(expr ast.Expr) (kind string, input string, output string, ok bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", "", "", false
	}

	name, typeArgs, ok := genericCall(call.Fun)
	if !ok || len(typeArgs) != 2 {
		return "", "", "", false
	}

	switch name {
	case "Query":
		kind = "query"
	case "Mutation":
		kind = "mutation"
	case "Subscription":
		kind = "subscription"
	default:
		return "", "", "", false
	}

	return kind, exprString(typeArgs[0]), exprString(typeArgs[1]), true
}

func genericCall(expr ast.Expr) (string, []ast.Expr, bool) {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		name, ok := selectorName(e.X)
		if !ok {
			return "", nil, false
		}
		return name, []ast.Expr{e.Index}, true
	case *ast.IndexListExpr:
		name, ok := selectorName(e.X)
		if !ok {
			return "", nil, false
		}
		return name, e.Indices, true
	default:
		return "", nil, false
	}
}

func selectorName(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name, true
	case *ast.SelectorExpr:
		return e.Sel.Name, true
	default:
		return "", false
	}
}

func selectorReceiverName(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name, true
	case *ast.SelectorExpr:
		return e.Sel.Name, true
	default:
		return "", false
	}
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func exprString(expr ast.Expr) string {
	var buf bytes.Buffer
	_ = format.Node(&buf, token.NewFileSet(), expr)
	return buf.String()
}

func generate(pkg string, procedures []procedure) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by neo-gen; DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\n")
	b.WriteString("\tneo \"github.com/Protocol-Lattice/neo\"\n")
	b.WriteString(")\n\n")

	groups := groupProcedures(procedures)

	b.WriteString("type TypedClient struct {\n")
	b.WriteString("\tclient *neo.Client\n")
	for _, group := range sortedKeys(groups) {
		fmt.Fprintf(&b, "\t%s %sClient\n", exportName(group), exportName(group))
	}
	for _, p := range procedures {
		if !strings.Contains(p.Key, ".") {
			fmt.Fprintf(&b, "\t%s %sProcedure\n", exportName(p.Key), procTypeName(p))
		}
	}
	b.WriteString("}\n\n")

	b.WriteString("func NewTypedClient(addr string, opts ...neo.ClientOption) *TypedClient {\n")
	b.WriteString("\tc := neo.NewClient(addr, opts...)\n")
	b.WriteString("\treturn NewTypedClientFromClient(c)\n")
	b.WriteString("}\n\n")

	b.WriteString("func NewTypedClientFromClient(c *neo.Client) *TypedClient {\n")
	b.WriteString("\ttc := &TypedClient{client: c}\n")
	for _, group := range sortedKeys(groups) {
		fmt.Fprintf(&b, "\ttc.%s = %sClient{client: c}\n", exportName(group), exportName(group))
		for _, p := range groups[group] {
			field := exportName(lastKeyPart(p.Key))
			fmt.Fprintf(&b, "\ttc.%s.%s = %sProcedure{client: c}\n", exportName(group), field, procTypeName(p))
		}
	}
	for _, p := range procedures {
		if !strings.Contains(p.Key, ".") {
			fmt.Fprintf(&b, "\ttc.%s = %sProcedure{client: c}\n", exportName(p.Key), procTypeName(p))
		}
	}
	b.WriteString("\treturn tc\n")
	b.WriteString("}\n\n")

	for _, group := range sortedKeys(groups) {
		fmt.Fprintf(&b, "type %sClient struct {\n", exportName(group))
		b.WriteString("\tclient *neo.Client\n")
		for _, p := range groups[group] {
			fmt.Fprintf(&b, "\t%s %sProcedure\n", exportName(lastKeyPart(p.Key)), procTypeName(p))
		}
		b.WriteString("}\n\n")
	}

	for _, p := range procedures {
		writeProcedureType(&b, p)
	}

	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return b.Bytes(), err
	}
	return formatted, nil
}

type typeScriptOptions struct {
	RuntimeImport string
	Standalone    bool
}

func generateTypeScript(procedures []procedure, types []typeDeclaration, opts typeScriptOptions) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by neo-gen; DO NOT EDIT.\n\n")
	if opts.Standalone {
		writeTypeScriptRuntime(&b)
	} else {
		if opts.RuntimeImport == "" {
			opts.RuntimeImport = "./neo.runtime.ts"
		}
		writeTypeScriptRuntimeImport(&b, opts.RuntimeImport)
	}
	writeTypeScriptExternalTypes(&b, collectExternalTypeRefs(procedures, types))
	writeTypeScriptTypes(&b, types)
	writeTypeScriptClient(&b, procedures)
	return b.Bytes(), nil
}

func generateTypeScriptRuntime() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by neo-gen; DO NOT EDIT.\n\n")
	writeTypeScriptRuntime(&b)
	return b.Bytes(), nil
}

func writeTypeScriptRuntimeImport(b *bytes.Buffer, runtimeImport string) {
	quoted := strconv.Quote(runtimeImport)
	fmt.Fprintf(b, "import { NeoClientCore, type NeoCallOptions, type NeoClientOptions } from %s;\n", quoted)
	fmt.Fprintf(b, "export { NeoError } from %s;\n", quoted)
	fmt.Fprintf(b, "export type { NeoCallOptions, NeoClientOptions, NeoHeaders } from %s;\n\n", quoted)
}

func writeTypeScriptRuntime(b *bytes.Buffer) {
	b.WriteString(`export type NeoHeaders = Record<string, string>;

export type NeoAbortSignal = {
  readonly aborted?: boolean;
  addEventListener?: (type: "abort", listener: () => void, options?: { once?: boolean }) => void;
};

export type NeoCallOptions = {
  headers?: NeoHeaders;
  signal?: NeoAbortSignal;
};

export type NeoRequestInit = {
  method?: string;
  headers?: NeoHeaders;
  body?: string;
  signal?: any;
};

export type NeoReadableStreamReader = {
  read(): Promise<{ done?: boolean; value?: Uint8Array }>;
  releaseLock?: () => void;
};

export type NeoReadableStream = {
  getReader(): NeoReadableStreamReader;
};

export type NeoFetchResponse = {
  readonly ok: boolean;
  readonly status: number;
  text(): Promise<string>;
  readonly body?: NeoReadableStream | null;
};

export type NeoFetch = (input: string, init?: NeoRequestInit) => Promise<NeoFetchResponse>;

export type NeoWebSocketMessageEvent = {
  data: unknown;
};

export interface NeoWebSocket {
  close(): void;
  addEventListener(type: "message", listener: (event: NeoWebSocketMessageEvent) => void): void;
  addEventListener(type: "error" | "close" | "open", listener: () => void): void;
}

export type NeoWebSocketConstructor = new (url: string) => NeoWebSocket;

export type NeoClientOptions = {
  fetch?: NeoFetch;
  headers?: NeoHeaders;
  webSocket?: NeoWebSocketConstructor;
};

type NeoResponse<T> = {
  result?: T;
  code?: string;
  error?: string;
};

declare const TextDecoder: {
  new (): {
    decode(input?: Uint8Array, options?: { stream?: boolean }): string;
  };
};

const maxGETInputBytes = 6 * 1024;

export class NeoError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = "NeoError";
    this.code = code;
    this.status = status;
  }
}

export class NeoClientCore {
  private readonly baseURL: string;
  private readonly fetchFn: NeoFetch;
  private readonly headers: NeoHeaders;
  private readonly webSocketCtor?: NeoWebSocketConstructor;

  constructor(addr: string, options: NeoClientOptions = {}) {
    this.baseURL = addr.replace(/\/+$/, "");
    const fetchFn = options.fetch ?? (globalThis as unknown as { fetch?: NeoFetch }).fetch;
    if (!fetchFn) {
      throw new Error("Neo TypeScript client requires a fetch implementation");
    }
    this.fetchFn = fetchFn;
    this.headers = options.headers ?? {};
    this.webSocketCtor = options.webSocket;
  }

`)
	writeTypeScriptTransportMethods(b)
	b.WriteString("}\n\n")
}

type externalTypeRef struct {
	Name  string
	Arity int
}

func collectExternalTypeRefs(procedures []procedure, types []typeDeclaration) []externalTypeRef {
	refs := make(map[string]int)
	for _, p := range procedures {
		collectExternalTypeRefsFromTypeString(p.Input, refs)
		collectExternalTypeRefsFromTypeString(p.Output, refs)
	}
	for _, typ := range types {
		collectExternalTypeRefsFromExpr(typ.Type, refs)
	}

	out := make([]externalTypeRef, 0, len(refs))
	for name, arity := range refs {
		out = append(out, externalTypeRef{Name: name, Arity: arity})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func collectExternalTypeRefsFromTypeString(typeName string, refs map[string]int) {
	expr, err := parser.ParseExpr(typeName)
	if err != nil {
		return
	}
	collectExternalTypeRefsFromExpr(expr, refs)
}

func collectExternalTypeRefsFromExpr(expr ast.Expr, refs map[string]int) {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		if !isKnownSelectorType(e) {
			recordExternalTypeRef(refs, typeScriptExternalTypeName(e), 0)
		}
	case *ast.StarExpr:
		collectExternalTypeRefsFromExpr(e.X, refs)
	case *ast.ArrayType:
		collectExternalTypeRefsFromExpr(e.Elt, refs)
	case *ast.MapType:
		collectExternalTypeRefsFromExpr(e.Key, refs)
		collectExternalTypeRefsFromExpr(e.Value, refs)
	case *ast.StructType:
		if e.Fields == nil {
			return
		}
		for _, field := range e.Fields.List {
			collectExternalTypeRefsFromExpr(field.Type, refs)
		}
	case *ast.ChanType:
		collectExternalTypeRefsFromExpr(e.Value, refs)
	case *ast.IndexExpr:
		if selector, ok := e.X.(*ast.SelectorExpr); ok && !isKnownSelectorType(selector) {
			recordExternalTypeRef(refs, typeScriptExternalTypeName(selector), 1)
		} else {
			collectExternalTypeRefsFromExpr(e.X, refs)
		}
		collectExternalTypeRefsFromExpr(e.Index, refs)
	case *ast.IndexListExpr:
		if selector, ok := e.X.(*ast.SelectorExpr); ok && !isKnownSelectorType(selector) {
			recordExternalTypeRef(refs, typeScriptExternalTypeName(selector), len(e.Indices))
		} else {
			collectExternalTypeRefsFromExpr(e.X, refs)
		}
		for _, index := range e.Indices {
			collectExternalTypeRefsFromExpr(index, refs)
		}
	case *ast.ParenExpr:
		collectExternalTypeRefsFromExpr(e.X, refs)
	}
}

func recordExternalTypeRef(refs map[string]int, name string, arity int) {
	if name == "" {
		return
	}
	if current, ok := refs[name]; !ok || current < arity {
		refs[name] = arity
	}
}

func writeTypeScriptExternalTypes(b *bytes.Buffer, refs []externalTypeRef) {
	for _, ref := range refs {
		if ref.Arity == 0 {
			fmt.Fprintf(b, "export type %s = unknown;\n", ref.Name)
			continue
		}

		params := make([]string, 0, ref.Arity)
		for i := 1; i <= ref.Arity; i++ {
			params = append(params, fmt.Sprintf("T%d = unknown", i))
		}
		fmt.Fprintf(b, "export type %s<%s> = unknown;\n", ref.Name, strings.Join(params, ", "))
	}
	if len(refs) > 0 {
		b.WriteString("\n")
	}
}

func writeTypeScriptTypes(b *bytes.Buffer, types []typeDeclaration) {
	if len(types) == 0 {
		return
	}

	for _, typ := range types {
		name := typeScriptTypeIdentifier(typ.Name)
		typeParams := typeScriptTypeParams(typ.TypeParams)

		structType, ok := typ.Type.(*ast.StructType)
		if !ok {
			fmt.Fprintf(b, "export type %s%s = %s;\n\n", name, typeParams, goExprToTypeScript(typ.Type))
			continue
		}

		fields := typeScriptStructFields(structType)
		if len(fields) == 0 {
			fmt.Fprintf(b, "export type %s%s = Record<string, never>;\n\n", name, typeParams)
			continue
		}

		fmt.Fprintf(b, "export interface %s%s {\n", name, typeParams)
		for _, field := range fields {
			optional := ""
			if field.Optional {
				optional = "?"
			}
			fmt.Fprintf(b, "  %s%s: %s;\n", typeScriptPropertyName(field.Name), optional, field.Type)
		}
		b.WriteString("}\n\n")
	}
}

func writeTypeScriptClient(b *bytes.Buffer, procedures []procedure) {
	procedures = append([]procedure(nil), procedures...)
	sort.Slice(procedures, func(i, j int) bool {
		return procedures[i].Key < procedures[j].Key
	})

	groups := groupProcedures(procedures)
	names := newTypeScriptNames(procedures, groups)

	b.WriteString("export class TypedClient extends NeoClientCore {\n")
	for _, group := range sortedKeys(groups) {
		fmt.Fprintf(b, "  readonly %s: %s;\n", names.GroupMember[group], names.GroupClass[group])
	}
	for _, p := range procedures {
		if !strings.Contains(p.Key, ".") {
			fmt.Fprintf(b, "  readonly %s: %s;\n", names.RootMember[p.Key], names.ProcedureClass[p.Key])
		}
	}
	b.WriteString("\n")

	b.WriteString("  constructor(addr: string, options: NeoClientOptions = {}) {\n")
	b.WriteString("    super(addr, options);\n")
	for _, group := range sortedKeys(groups) {
		fmt.Fprintf(b, "    this.%s = new %s(this);\n", names.GroupMember[group], names.GroupClass[group])
	}
	for _, p := range procedures {
		if !strings.Contains(p.Key, ".") {
			fmt.Fprintf(b, "    this.%s = new %s(this);\n", names.RootMember[p.Key], names.ProcedureClass[p.Key])
		}
	}
	b.WriteString("  }\n\n")
	b.WriteString("}\n\n")
	b.WriteString("export function createClient(addr: string, options?: NeoClientOptions): TypedClient {\n")
	b.WriteString("  return new TypedClient(addr, options);\n")
	b.WriteString("}\n\n")

	for _, group := range sortedKeys(groups) {
		fmt.Fprintf(b, "export class %s {\n", names.GroupClass[group])
		for _, p := range groups[group] {
			fmt.Fprintf(b, "  readonly %s: %s;\n", names.ProcedureMember[p.Key], names.ProcedureClass[p.Key])
		}
		b.WriteString("\n")
		fmt.Fprintf(b, "  constructor(private readonly client: TypedClient) {\n")
		for _, p := range groups[group] {
			fmt.Fprintf(b, "    this.%s = new %s(client);\n", names.ProcedureMember[p.Key], names.ProcedureClass[p.Key])
		}
		b.WriteString("  }\n")
		b.WriteString("}\n\n")
	}

	for _, p := range procedures {
		writeTypeScriptProcedureType(b, p, names.ProcedureClass[p.Key])
	}
}

func writeTypeScriptTransportMethods(b *bytes.Buffer) {
	b.WriteString(`  async request<In, Out>(
    method: "GET" | "POST",
    key: string,
    input: In,
    options: NeoCallOptions = {},
  ): Promise<Out> {
    let url = this.urlFor(key);
    let requestMethod: "GET" | "POST" = method;
    let body: string | undefined;

    if (method === "GET") {
      if (input !== undefined && input !== null) {
        const rawInput = JSON.stringify(input);
        if (rawInput === undefined) {
          throw new Error("Neo input must be JSON serializable");
        }
        if (rawInput.length > maxGETInputBytes) {
          requestMethod = "POST";
          body = JSON.stringify({ input });
        } else {
          url += (url.includes("?") ? "&" : "?") + "input=" + encodeURIComponent(rawInput);
        }
      }
    } else {
      body = JSON.stringify({ input });
    }

    const response = await this.fetchFn(url, {
      method: requestMethod,
      headers: this.mergeHeaders(options.headers),
      body,
      signal: options.signal,
    });
    const payload = await this.readResponse<Out>(response);
    if (!response.ok || payload.error || payload.code) {
      throw this.toError(payload, response.status);
    }
    return payload.result as Out;
  }

  async *subscribe<In, Out>(
    key: string,
    input: In,
    options: NeoCallOptions = {},
  ): AsyncIterable<Out> {
    const response = await this.fetchFn(this.subscriptionURL(key, input), {
      method: "GET",
      headers: this.mergeHeaders(options.headers, "application/x-ndjson"),
      signal: options.signal,
    });
    if (!response.ok) {
      const payload = await this.readResponse<Out>(response);
      throw this.toError(payload, response.status);
    }
    if (!response.body) {
      throw new Error("Neo subscription response does not expose a readable body");
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    try {
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) {
          break;
        }
        buffer += decoder.decode(chunk.value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() ?? "";
        for (const line of lines) {
          const decoded = this.decodeStreamLine<Out>(line);
          if (decoded.hasValue) {
            yield decoded.value;
          }
        }
      }

      buffer += decoder.decode();
      const decoded = this.decodeStreamLine<Out>(buffer);
      if (decoded.hasValue) {
        yield decoded.value;
      }
    } finally {
      reader.releaseLock?.();
    }
  }

  async *subscribeWebSocket<In, Out>(
    key: string,
    input: In,
    options: NeoCallOptions = {},
  ): AsyncIterable<Out> {
    const WebSocketCtor = this.webSocketCtor ?? (globalThis as unknown as { WebSocket?: NeoWebSocketConstructor }).WebSocket;
    if (!WebSocketCtor) {
      throw new Error("Neo WebSocket subscriptions require a WebSocket implementation");
    }

    const socket = new WebSocketCtor(this.webSocketURL(key, input));
    const queue: Out[] = [];
    let done = false;
    let failure: Error | undefined;
    let wake: (() => void) | undefined;
    const notify = () => {
      const resolve = wake;
      wake = undefined;
      resolve?.();
    };

    const abort = () => {
      failure = new Error("Neo request aborted");
      done = true;
      socket.close();
      notify();
    };
    if (options.signal?.aborted) {
      abort();
    } else {
      options.signal?.addEventListener?.("abort", abort, { once: true });
    }

    socket.addEventListener("message", (event) => {
      if (typeof event.data !== "string") {
        return;
      }
      let payload: NeoResponse<Out>;
      try {
        payload = JSON.parse(event.data) as NeoResponse<Out>;
      } catch {
        failure = new Error("Neo WebSocket message was not valid JSON");
        done = true;
        socket.close();
        notify();
        return;
      }
      if (payload.error || payload.code) {
        failure = this.toError(payload, 0);
        done = true;
        socket.close();
        notify();
        return;
      }
      queue.push(payload.result as Out);
      notify();
    });
    socket.addEventListener("error", () => {
      failure = new Error("Neo WebSocket subscription failed");
      done = true;
      notify();
    });
    socket.addEventListener("close", () => {
      done = true;
      notify();
    });

    try {
      while (!done || queue.length > 0) {
        if (queue.length > 0) {
          yield queue.shift() as Out;
          continue;
        }
        if (failure) {
          throw failure;
        }
        await new Promise<void>((resolve) => {
          wake = resolve;
        });
      }
      if (failure) {
        throw failure;
      }
    } finally {
      socket.close();
    }
  }

  private urlFor(key: string): string {
    return this.baseURL + "/" + key.replace(/^\/+|\/+$/g, "");
  }

  private subscriptionURL<In>(key: string, input: In): string {
    let url = this.urlFor(key);
    if (input === undefined || input === null) {
      return url;
    }
    const rawInput = JSON.stringify(input);
    if (rawInput === undefined) {
      throw new Error("Neo input must be JSON serializable");
    }
    return url + (url.includes("?") ? "&" : "?") + "input=" + encodeURIComponent(rawInput);
  }

  private webSocketURL<In>(key: string, input: In): string {
    const url = this.subscriptionURL(key, input);
    if (url.startsWith("https://")) {
      return "wss://" + url.slice("https://".length);
    }
    if (url.startsWith("http://")) {
      return "ws://" + url.slice("http://".length);
    }
    return url;
  }

  private mergeHeaders(extra?: NeoHeaders, accept?: string): NeoHeaders {
    const headers: NeoHeaders = { "Content-Type": "application/json" };
    if (accept) {
      headers.Accept = accept;
    }
    for (const name in this.headers) {
      headers[name] = this.headers[name];
    }
    for (const name in extra ?? {}) {
      headers[name] = extra[name];
    }
    return headers;
  }

  private async readResponse<Out>(response: NeoFetchResponse): Promise<NeoResponse<Out>> {
    const text = await response.text();
    if (text === "") {
      return {};
    }
    try {
      return JSON.parse(text) as NeoResponse<Out>;
    } catch {
      throw new Error("Neo response was not valid JSON");
    }
  }

  private decodeStreamLine<Out>(line: string): { hasValue: boolean; value: Out } {
    const trimmed = line.trim();
    if (trimmed === "") {
      return { hasValue: false, value: undefined as Out };
    }
    let payload: NeoResponse<Out>;
    try {
      payload = JSON.parse(trimmed) as NeoResponse<Out>;
    } catch {
      throw new Error("Neo subscription message was not valid JSON");
    }
    if (payload.error || payload.code) {
      throw this.toError(payload, 0);
    }
    return { hasValue: true, value: payload.result as Out };
  }

  private toError(payload: NeoResponse<unknown>, status: number): NeoError {
    return new NeoError(payload.code ?? "INTERNAL", payload.error ?? "procedure failed", status);
  }

`)
}

func writeTypeScriptProcedureType(b *bytes.Buffer, p procedure, typeName string) {
	input := typeScriptProcedureType(p.Input)
	output := typeScriptProcedureType(p.Output)

	fmt.Fprintf(b, "export class %s {\n", typeName)
	b.WriteString("  constructor(private readonly client: TypedClient) {}\n\n")

	switch p.Kind {
	case "query":
		fmt.Fprintf(b, "  query(input: %s, options?: NeoCallOptions): Promise<%s> {\n", input, output)
		b.WriteString("    return this.call(input, options);\n")
		b.WriteString("  }\n\n")
		fmt.Fprintf(b, "  call(input: %s, options?: NeoCallOptions): Promise<%s> {\n", input, output)
		fmt.Fprintf(b, "    return this.client.request<%s, %s>(\"GET\", %q, input, options);\n", input, output, p.Key)
		b.WriteString("  }\n")
	case "mutation":
		fmt.Fprintf(b, "  mutate(input: %s, options?: NeoCallOptions): Promise<%s> {\n", input, output)
		b.WriteString("    return this.call(input, options);\n")
		b.WriteString("  }\n\n")
		fmt.Fprintf(b, "  call(input: %s, options?: NeoCallOptions): Promise<%s> {\n", input, output)
		fmt.Fprintf(b, "    return this.client.request<%s, %s>(\"POST\", %q, input, options);\n", input, output, p.Key)
		b.WriteString("  }\n")
	case "subscription":
		fmt.Fprintf(b, "  subscribe(input: %s, options?: NeoCallOptions): AsyncIterable<%s> {\n", input, output)
		fmt.Fprintf(b, "    return this.client.subscribe<%s, %s>(%q, input, options);\n", input, output, p.Key)
		b.WriteString("  }\n\n")
		fmt.Fprintf(b, "  subscribeWebSocket(input: %s, options?: NeoCallOptions): AsyncIterable<%s> {\n", input, output)
		fmt.Fprintf(b, "    return this.client.subscribeWebSocket<%s, %s>(%q, input, options);\n", input, output, p.Key)
		b.WriteString("  }\n")
	}

	b.WriteString("}\n\n")
}

func writeProcedureType(b *bytes.Buffer, p procedure) {
	typeName := procTypeName(p)
	fmt.Fprintf(b, "type %sProcedure struct {\n", typeName)
	b.WriteString("\tclient *neo.Client\n")
	b.WriteString("}\n\n")

	switch p.Kind {
	case "query":
		fmt.Fprintf(b, "func (p %sProcedure) Query(ctx context.Context, input %s) (%s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn p.Call(ctx, input)\n")
		b.WriteString("}\n\n")
		fmt.Fprintf(b, "func (p %sProcedure) Call(ctx context.Context, input %s) (%s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn neo.CallTyped[%s, %s](ctx, p.client.Query.Procedure(\"%s\"), input)\n", p.Input, p.Output, p.Key)
		b.WriteString("}\n\n")
	case "mutation":
		fmt.Fprintf(b, "func (p %sProcedure) Mutate(ctx context.Context, input %s) (%s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn p.Call(ctx, input)\n")
		b.WriteString("}\n\n")
		fmt.Fprintf(b, "func (p %sProcedure) Call(ctx context.Context, input %s) (%s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn neo.CallTyped[%s, %s](ctx, p.client.Mutation.Procedure(\"%s\"), input)\n", p.Input, p.Output, p.Key)
		b.WriteString("}\n\n")
	case "subscription":
		fmt.Fprintf(b, "func (p %sProcedure) Subscribe(ctx context.Context, input %s) (<-chan %s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn neo.SubscribeTyped[%s, %s](ctx, p.client.Subscription.Procedure(\"%s\"), input)\n", p.Input, p.Output, p.Key)
		b.WriteString("}\n\n")
		fmt.Fprintf(b, "func (p %sProcedure) SubscribeWebSocket(ctx context.Context, input %s) (<-chan %s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn neo.SubscribeWebSocketTyped[%s, %s](ctx, p.client.Subscription.Procedure(\"%s\"), input)\n", p.Input, p.Output, p.Key)
		b.WriteString("}\n\n")
	}
}

func groupProcedures(procedures []procedure) map[string][]procedure {
	groups := make(map[string][]procedure)
	for _, p := range procedures {
		parts := strings.Split(p.Key, ".")
		if len(parts) < 2 {
			continue
		}
		groups[parts[0]] = append(groups[parts[0]], p)
	}
	return groups
}

type typeScriptNames struct {
	GroupMember     map[string]string
	GroupClass      map[string]string
	RootMember      map[string]string
	ProcedureMember map[string]string
	ProcedureClass  map[string]string
}

func newTypeScriptNames(procedures []procedure, groups map[string][]procedure) typeScriptNames {
	names := typeScriptNames{
		GroupMember:     make(map[string]string),
		GroupClass:      make(map[string]string),
		RootMember:      make(map[string]string),
		ProcedureMember: make(map[string]string),
		ProcedureClass:  make(map[string]string),
	}

	topMembers := make(map[string]int)
	classNames := make(map[string]int)
	for _, group := range sortedKeys(groups) {
		names.GroupMember[group] = uniqueTypeScriptName(typeScriptMemberName(group), topMembers)
		names.GroupClass[group] = uniqueTypeScriptName(typeScriptClassName(group)+"Client", classNames)
	}

	for _, p := range procedures {
		if !strings.Contains(p.Key, ".") {
			names.RootMember[p.Key] = uniqueTypeScriptName(typeScriptMemberName(p.Key), topMembers)
		}
		names.ProcedureClass[p.Key] = uniqueTypeScriptName(typeScriptProcedureClassName(p)+"Procedure", classNames)
	}

	for _, group := range sortedKeys(groups) {
		groupMembers := make(map[string]int)
		for _, p := range groups[group] {
			names.ProcedureMember[p.Key] = uniqueTypeScriptName(typeScriptMemberName(lastKeyPart(p.Key)), groupMembers)
		}
	}

	return names
}

func uniqueTypeScriptName(base string, used map[string]int) string {
	if base == "" {
		base = "value"
	}

	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return base
	}
	return fmt.Sprintf("%s%d", base, count+1)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func procTypeName(p procedure) string {
	parts := strings.Split(p.Key, ".")
	for i, part := range parts {
		parts[i] = exportName(part)
	}
	return strings.Join(parts, "")
}

func lastKeyPart(key string) string {
	parts := strings.Split(key, ".")
	return parts[len(parts)-1]
}

func exportName(s string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range s {
		if r == '.' || r == '_' || r == '-' || r == '/' {
			upperNext = true
			continue
		}
		if upperNext {
			b.WriteString(strings.ToUpper(string(r)))
			upperNext = false
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if out == "" {
		return "Procedure"
	}
	return out
}

type typeScriptField struct {
	Name     string
	Type     string
	Optional bool
}

func typeScriptStructFields(structType *ast.StructType) []typeScriptField {
	if structType.Fields == nil {
		return nil
	}

	var fields []typeScriptField
	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			continue
		}

		fieldType := goExprToTypeScript(field.Type)
		tagName, optional, stringEncoded, skip := jsonFieldTag(field)
		if skip {
			continue
		}
		if stringEncoded {
			fieldType = "string"
		}

		for i, name := range field.Names {
			if name == nil || !ast.IsExported(name.Name) {
				continue
			}

			jsonName := name.Name
			if tagName != "" {
				jsonName = tagName
				if len(field.Names) > 1 && i > 0 {
					jsonName = name.Name
				}
			}

			fields = append(fields, typeScriptField{
				Name:     jsonName,
				Type:     fieldType,
				Optional: optional,
			})
		}
	}

	return fields
}

func jsonFieldTag(field *ast.Field) (name string, optional bool, stringEncoded bool, skip bool) {
	if field.Tag == nil {
		return "", false, false, false
	}

	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", false, false, false
	}

	tag := reflect.StructTag(raw).Get("json")
	if tag == "" {
		return "", false, false, false
	}

	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", false, false, true
	}

	for _, opt := range parts[1:] {
		switch opt {
		case "omitempty", "omitzero":
			optional = true
		case "string":
			stringEncoded = true
		}
	}

	return parts[0], optional, stringEncoded, false
}

func goExprToTypeScript(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return goIdentToTypeScript(e.Name)
	case *ast.SelectorExpr:
		return goSelectorToTypeScript(e)
	case *ast.StarExpr:
		return goExprToTypeScript(e.X) + " | null"
	case *ast.ArrayType:
		return typeScriptArrayType(goExprToTypeScript(e.Elt))
	case *ast.MapType:
		return fmt.Sprintf("Record<string, %s>", goExprToTypeScript(e.Value))
	case *ast.InterfaceType:
		return "unknown"
	case *ast.StructType:
		return typeScriptStructLiteral(e)
	case *ast.ChanType:
		return fmt.Sprintf("AsyncIterable<%s>", goExprToTypeScript(e.Value))
	case *ast.FuncType:
		return "unknown"
	case *ast.IndexExpr:
		return fmt.Sprintf("%s<%s>", goExprToTypeScript(e.X), goExprToTypeScript(e.Index))
	case *ast.IndexListExpr:
		args := make([]string, 0, len(e.Indices))
		for _, index := range e.Indices {
			args = append(args, goExprToTypeScript(index))
		}
		return fmt.Sprintf("%s<%s>", goExprToTypeScript(e.X), strings.Join(args, ", "))
	case *ast.ParenExpr:
		return goExprToTypeScript(e.X)
	default:
		return "unknown"
	}
}

func goIdentToTypeScript(name string) string {
	switch name {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "complex64", "complex128",
		"byte", "rune":
		return "number"
	case "any":
		return "unknown"
	default:
		return typeScriptTypeIdentifier(name)
	}
}

func goSelectorToTypeScript(expr *ast.SelectorExpr) string {
	if known, ok := knownSelectorTypeScript(expr); ok {
		return known
	}
	return typeScriptExternalTypeName(expr)
}

func knownSelectorTypeScript(expr *ast.SelectorExpr) (string, bool) {
	switch exprString(expr) {
	case "time.Time":
		return "string", true
	case "json.RawMessage":
		return "unknown", true
	default:
		return "", false
	}
}

func isKnownSelectorType(expr *ast.SelectorExpr) bool {
	_, ok := knownSelectorTypeScript(expr)
	return ok
}

func typeScriptExternalTypeName(expr *ast.SelectorExpr) string {
	return typeScriptTypeIdentifier(exprString(expr))
}

func typeScriptStructLiteral(structType *ast.StructType) string {
	fields := typeScriptStructFields(structType)
	if len(fields) == 0 {
		return "Record<string, never>"
	}

	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		optional := ""
		if field.Optional {
			optional = "?"
		}
		parts = append(parts, fmt.Sprintf("%s%s: %s", typeScriptPropertyName(field.Name), optional, field.Type))
	}

	return "{ " + strings.Join(parts, "; ") + " }"
}

func typeScriptArrayType(inner string) string {
	if strings.Contains(inner, "|") || strings.Contains(inner, "&") {
		return "Array<" + inner + ">"
	}
	return inner + "[]"
}

func typeScriptProcedureType(goType string) string {
	expr, err := parser.ParseExpr(goType)
	if err != nil {
		return "unknown"
	}
	return goExprToTypeScript(expr)
}

func typeScriptProcedureClassName(p procedure) string {
	return typeScriptClassName(procTypeName(p))
}

func typeScriptClassName(s string) string {
	name := exportName(s)
	if !isTypeScriptIdentifier(name) || isTypeScriptReservedWord(name) {
		name = "Neo" + name
	}
	if name == "" {
		return "NeoProcedure"
	}
	if !isTypeScriptIdentifierStart(name[0]) {
		name = "Neo" + name
	}
	return name
}

func typeScriptMemberName(s string) string {
	name := exportName(s)
	if name == "" {
		name = "procedure"
	}
	name = lowerFirst(name)
	if !isTypeScriptIdentifier(name) || isTypeScriptReservedWord(name) {
		name += "_"
	}
	if !isTypeScriptIdentifierStart(name[0]) {
		name = "_" + name
	}
	return name
}

func typeScriptTypeIdentifier(name string) string {
	if isTypeScriptIdentifier(name) && !isTypeScriptReservedWord(name) {
		return name
	}
	name = exportName(name)
	if name == "" {
		name = "NeoType"
	}
	if !isTypeScriptIdentifierStart(name[0]) {
		name = "Neo" + name
	}
	if isTypeScriptReservedWord(name) {
		name += "Type"
	}
	return name
}

func typeScriptTypeParams(params []string) string {
	if len(params) == 0 {
		return ""
	}

	names := make([]string, 0, len(params))
	for _, param := range params {
		names = append(names, typeScriptTypeIdentifier(param))
	}
	return "<" + strings.Join(names, ", ") + ">"
}

func typeScriptPropertyName(name string) string {
	if isTypeScriptIdentifier(name) && !isTypeScriptReservedWord(name) {
		return name
	}
	return strconv.Quote(name)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	first := s[0]
	if first >= 'A' && first <= 'Z' {
		return string(first+'a'-'A') + s[1:]
	}
	return s
}

func isTypeScriptIdentifier(s string) bool {
	if s == "" || !isTypeScriptIdentifierStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isTypeScriptIdentifierPart(s[i]) {
			return false
		}
	}
	return true
}

func isTypeScriptIdentifierStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || ch == '$'
}

func isTypeScriptIdentifierPart(ch byte) bool {
	return isTypeScriptIdentifierStart(ch) || (ch >= '0' && ch <= '9')
}

func isTypeScriptReservedWord(s string) bool {
	switch s {
	case "break", "case", "catch", "class", "const", "continue", "debugger",
		"default", "delete", "do", "else", "enum", "export", "extends",
		"false", "finally", "for", "function", "if", "import", "in",
		"instanceof", "new", "null", "return", "super", "switch", "this",
		"throw", "true", "try", "typeof", "var", "void", "while", "with",
		"as", "implements", "interface", "let", "package", "private",
		"protected", "public", "static", "yield", "any", "boolean",
		"constructor", "declare", "get", "module", "require", "number",
		"set", "string", "symbol", "type", "from", "of":
		return true
	default:
		return false
	}
}
