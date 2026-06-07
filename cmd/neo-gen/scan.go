package main

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
		return packageScan{}, fmt.Errorf("no go package found in %s", dir)
	}

	var pkgName string
	var files []*ast.File
	for _, name := range sortedKeys(packages) {
		pkg := packages[name]
		if strings.HasSuffix(name, "_test") {
			continue
		}
		pkgName = name
		for _, file := range pkg.Files {
			files = append(files, file)
		}
		break
	}
	if pkgName == "" {
		return packageScan{}, fmt.Errorf("no go package found in %s", dir)
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

			if parent, prefix, child, ok := routerPrefixCall(call); ok {
				nested = append(nested, nestedRouter{Parent: parent, Prefix: prefix, Child: child})
				seenReceivers[parent] = struct{}{}
				seenReceivers[child] = struct{}{}
				return true
			}

			if receiver, proxied, ok := gatewayProxyCall(call); ok {
				seenReceivers[receiver] = struct{}{}
				procedures = append(procedures, proxied...)
				return true
			}

			procedure, ok := registerCallProcedure(call)
			if !ok {
				return true
			}

			seenReceivers[procedure.Receiver] = struct{}{}
			procedures = append(procedures, procedure)

			return true
		})
	}

	prefixes, err := inferPrefixes(seenReceivers, nested)
	if err != nil {
		return packageScan{}, err
	}
	for i := range procedures {
		procedures[i].Key = fullProcedureKey(prefixes[procedures[i].Receiver], procedures[i].Key)
	}

	slices.SortFunc(procedures, func(a, b procedure) int {
		return cmp.Compare(a.Key, b.Key)
	})
	slices.SortFunc(types, func(a, b typeDeclaration) int {
		return cmp.Compare(a.Name, b.Name)
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
	p, ok := registerCallProcedure(call)
	if !ok {
		return "", "", "", "", "", false
	}

	return p.Receiver, p.Key, p.Kind, p.Input, p.Output, true
}

func registerCallProcedure(call *ast.CallExpr) (procedure, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Register" && sel.Sel.Name != "RegisterSubscription") || len(call.Args) < 2 {
		return procedure{}, false
	}

	receiver, ok := selectorReceiverName(sel.X)
	if !ok {
		return procedure{}, false
	}

	key, ok := stringLiteral(call.Args[0])
	if !ok || key == "" {
		return procedure{}, false
	}

	kind, input, output, meta, ok := typedProcedureMetadata(call.Args[1])
	if !ok {
		return procedure{}, false
	}

	meta.Receiver = receiver
	meta.Key = key
	meta.Kind = kind
	meta.Input = input
	meta.Output = output
	return meta, true
}

func routerPrefixCall(call *ast.CallExpr) (parent, prefix, child string, ok bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Nested" && sel.Sel.Name != "Mount") || len(call.Args) < 2 {
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

func nestedCall(call *ast.CallExpr) (parent, prefix, child string, ok bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Nested" {
		return "", "", "", false
	}
	return routerPrefixCall(call)
}

func gatewayProxyCall(call *ast.CallExpr) (receiver string, procedures []procedure, ok bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Proxy" || len(call.Args) < 3 {
		return "", nil, false
	}

	receiver, ok = selectorReceiverName(sel.X)
	if !ok {
		return "", nil, false
	}

	prefix, ok := stringLiteral(call.Args[0])
	if !ok || prefix == "" {
		return "", nil, false
	}

	for _, arg := range call.Args[2:] {
		procedures = append(procedures, proxyMetadataProcedures(receiver, prefix, arg)...)
	}
	if len(procedures) == 0 {
		return "", nil, false
	}

	return receiver, procedures, true
}

func inferPrefixes(receivers map[string]struct{}, nested []nestedRouter) (map[string]string, error) {
	prefixes := make(map[string]string)
	for receiver := range receivers {
		prefixes[receiver] = ""
	}

	children := make(map[string][]nestedRouter)
	childReceivers := make(map[string]struct{})
	for _, n := range nested {
		children[n.Parent] = append(children[n.Parent], n)
		childReceivers[n.Child] = struct{}{}
	}
	for parent := range children {
		slices.SortFunc(children[parent], func(a, b nestedRouter) int {
			if n := cmp.Compare(a.Prefix, b.Prefix); n != 0 {
				return n
			}
			return cmp.Compare(a.Child, b.Child)
		})
	}

	roots := make([]string, 0, len(receivers))
	for receiver := range receivers {
		if _, isChild := childReceivers[receiver]; !isChild {
			roots = append(roots, receiver)
		}
	}
	slices.Sort(roots)

	assigned := make(map[string]string, len(receivers))
	visiting := make(map[string]bool, len(receivers))
	visited := make(map[string]bool, len(receivers))

	var walk func(receiver string, prefix string) error
	walk = func(receiver string, prefix string) error {
		if visiting[receiver] {
			return fmt.Errorf("nested router cycle involving %q", receiver)
		}
		if previous, ok := assigned[receiver]; ok && previous != prefix {
			return fmt.Errorf("nested router %q has conflicting prefixes %q and %q", receiver, previous, prefix)
		}
		if visited[receiver] {
			return nil
		}

		assigned[receiver] = prefix
		prefixes[receiver] = prefix
		visiting[receiver] = true
		defer func() {
			visiting[receiver] = false
			visited[receiver] = true
		}()

		for _, n := range children[receiver] {
			if err := walk(n.Child, joinKey(prefix, n.Prefix)); err != nil {
				return err
			}
		}

		return nil
	}

	for _, root := range roots {
		if err := walk(root, ""); err != nil {
			return nil, err
		}
	}
	for _, receiver := range sortedKeys(receivers) {
		if _, ok := assigned[receiver]; ok {
			continue
		}
		if err := walk(receiver, prefixes[receiver]); err != nil {
			return nil, err
		}
	}

	return prefixes, nil
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
	kind, input, output, _, ok = typedProcedureMetadata(expr)
	return kind, input, output, ok
}

func typedProcedureMetadata(expr ast.Expr) (kind string, input string, output string, meta procedure, ok bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", "", "", procedure{}, false
	}

	name, typeArgs, ok := genericCall(call.Fun)
	if !ok || len(typeArgs) != 2 {
		return "", "", "", procedure{}, false
	}

	switch name {
	case "Query":
		kind = "query"
	case "Mutation":
		kind = "mutation"
	case "Subscription":
		kind = "subscription"
	default:
		return "", "", "", procedure{}, false
	}

	meta = procedureOptions(call.Args)
	return kind, exprString(typeArgs[0]), exprString(typeArgs[1]), meta, true
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

func boolLiteral(expr ast.Expr) (bool, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false, false
	}
	switch ident.Name {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func stringSliceLiteral(expr ast.Expr) ([]string, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}

	var values []string
	for _, elt := range lit.Elts {
		value, ok := stringLiteral(elt)
		if !ok {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func exprString(expr ast.Expr) string {
	var buf bytes.Buffer
	_ = format.Node(&buf, token.NewFileSet(), expr)
	return buf.String()
}
