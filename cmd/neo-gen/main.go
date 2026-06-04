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
	"sort"
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

func main() {
	dir := flag.String("dir", ".", "directory containing procedure registrations")
	out := flag.String("out", "neo.gen.go", "output file")
	pkgOverride := flag.String("package", "", "optional generated package name")
	flag.Parse()

	pkg, procedures, err := scanDir(*dir)
	if err != nil {
		log.Fatalf("neo-gen: %v", err)
	}
	if *pkgOverride != "" {
		pkg = *pkgOverride
	}
	if pkg == "" {
		log.Fatalf("neo-gen: could not determine package for %s", *dir)
	}
	if len(procedures) == 0 {
		log.Fatalf("neo-gen: no typed procedures found in %s; expected neo.Query[In, Out](...), neo.Mutation[In, Out](...), or neo.Subscription[In, Out](...) inside Register/RegisterSubscription", *dir)
	}

	src, err := generate(pkg, procedures)
	if err != nil {
		log.Fatalf("neo-gen: generate: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("neo-gen: create output dir: %v", err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		log.Fatalf("neo-gen: write %s: %v", *out, err)
	}

	fmt.Printf("neo-gen: generated %d typed procedures in %s\n", len(procedures), *out)
}

func scanDir(dir string) (string, []procedure, error) {
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, dir, func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, ".gen.go")
	}, parser.ParseComments)
	if err != nil {
		return "", nil, err
	}
	if len(packages) == 0 {
		return "", nil, fmt.Errorf("no Go package found in %s", dir)
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
	var nested []nestedRouter
	seenReceivers := make(map[string]struct{})

	for _, file := range files {
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

	return pkgName, procedures, nil
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
	return strings.Trim(lit.Value, "`\""), true
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
	b.WriteString("\tneo \"neo\"\n")
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

	b.WriteString("func NewTypedClient(addr string) *TypedClient {\n")
	b.WriteString("\tc := neo.NewClient(addr)\n")
	b.WriteString("\treturn newTypedClient(c)\n")
	b.WriteString("}\n\n")

	b.WriteString("func newTypedClient(c *neo.Client) *TypedClient {\n")
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

func writeProcedureType(b *bytes.Buffer, p procedure) {
	typeName := procTypeName(p)
	fmt.Fprintf(b, "type %sProcedure struct {\n", typeName)
	b.WriteString("\tclient *neo.Client\n")
	b.WriteString("}\n\n")

	switch p.Kind {
	case "query":
		fmt.Fprintf(b, "func (p %sProcedure) Query(ctx context.Context, input %s) (%s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn neo.CallTyped[%s, %s](ctx, p.client.Query.Procedure(\"%s\"), input)\n", p.Input, p.Output, p.Key)
		b.WriteString("}\n\n")
	case "mutation":
		fmt.Fprintf(b, "func (p %sProcedure) Mutate(ctx context.Context, input %s) (%s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn neo.CallTyped[%s, %s](ctx, p.client.Mutation.Procedure(\"%s\"), input)\n", p.Input, p.Output, p.Key)
		b.WriteString("}\n\n")
	case "subscription":
		fmt.Fprintf(b, "func (p %sProcedure) Subscribe(ctx context.Context, input %s) (<-chan %s, error) {\n", typeName, p.Input, p.Output)
		fmt.Fprintf(b, "\treturn neo.SubscribeTyped[%s, %s](ctx, p.client.Subscription.Procedure(\"%s\"), input)\n", p.Input, p.Output, p.Key)
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
