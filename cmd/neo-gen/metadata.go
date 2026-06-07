package main

import "go/ast"

func proxyMetadataProcedures(receiver string, prefix string, expr ast.Expr) []procedure {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}

	name, ok := selectorName(call.Fun)
	if !ok || name != "WithProxyMetadata" {
		return nil
	}

	var procedures []procedure
	for _, arg := range call.Args {
		if p, ok := procedureMeta(receiver, prefix, arg); ok {
			procedures = append(procedures, p)
		}
	}
	return procedures
}

func procedureMeta(receiver string, prefix string, expr ast.Expr) (procedure, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return procedure{}, false
	}

	typeName, ok := selectorName(lit.Type)
	if !ok || typeName != "ProcedureMeta" {
		return procedure{}, false
	}

	p := procedureMetaLiteral(lit)
	p.Receiver = receiver

	if p.Key == "" || p.Input == "" || p.Output == "" {
		return procedure{}, false
	}
	if p.Kind == "" {
		p.Kind = "query"
	}
	p.Key = fullProcedureKey(prefix, p.Key)
	return p, true
}

func procedureMetaLiteral(lit *ast.CompositeLit) procedure {
	var p procedure
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}

		switch key.Name {
		case "Key":
			p.Key, _ = stringLiteral(kv.Value)
		case "Kind":
			p.Kind, _ = procedureKind(kv.Value)
		case "Input":
			p.Input, _ = stringLiteral(kv.Value)
		case "Output":
			p.Output, _ = stringLiteral(kv.Value)
		case "Summary":
			p.Summary, _ = stringLiteral(kv.Value)
		case "Description":
			p.Description, _ = stringLiteral(kv.Value)
		case "Tags":
			p.Tags, _ = stringSliceLiteral(kv.Value)
		case "Deprecated":
			p.Deprecated, _ = boolLiteral(kv.Value)
		}
	}
	return p
}

func procedureKind(expr ast.Expr) (string, bool) {
	if value, ok := stringLiteral(expr); ok {
		switch value {
		case "query", "mutation", "subscription":
			return value, true
		default:
			return "", false
		}
	}

	name, ok := selectorName(expr)
	if !ok {
		return "", false
	}

	switch name {
	case "ProcedureKindQuery":
		return "query", true
	case "ProcedureKindMutation":
		return "mutation", true
	case "ProcedureKindSubscription":
		return "subscription", true
	default:
		return "", false
	}
}

func procedureOptions(args []ast.Expr) procedure {
	var meta procedure
	for _, arg := range args {
		call, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		name, ok := selectorName(call.Fun)
		if !ok {
			continue
		}

		switch name {
		case "WithSummary":
			if len(call.Args) > 0 {
				meta.Summary, _ = stringLiteral(call.Args[0])
			}
		case "WithDescription":
			if len(call.Args) > 0 {
				meta.Description, _ = stringLiteral(call.Args[0])
			}
		case "WithTags":
			for _, arg := range call.Args {
				if tag, ok := stringLiteral(arg); ok {
					meta.Tags = append(meta.Tags, tag)
				}
			}
		case "WithDeprecated":
			meta.Deprecated = true
			if len(call.Args) > 0 {
				meta.Deprecated, _ = boolLiteral(call.Args[0])
			}
		case "WithProcedureMeta":
			if len(call.Args) == 0 {
				continue
			}
			lit, ok := call.Args[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			typeName, ok := selectorName(lit.Type)
			if !ok || typeName != "ProcedureMeta" {
				continue
			}
			mergeProcedureMetadata(&meta, procedureMetaLiteral(lit))
		}
	}
	return meta
}

func mergeProcedureMetadata(target *procedure, source procedure) {
	if source.Summary != "" {
		target.Summary = source.Summary
	}
	if source.Description != "" {
		target.Description = source.Description
	}
	if len(source.Tags) > 0 {
		target.Tags = append(target.Tags, source.Tags...)
	}
	if source.Deprecated {
		target.Deprecated = true
	}
}
