package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"strings"
	"unicode"
)

const (
	openAPIJSONContentType   = "application/json"
	openAPINDJSONContentType = "application/x-ndjson"
)

type openAPIDocument struct {
	OpenAPI    string            `json:"openapi"`
	Info       openAPIInfo       `json:"info"`
	Paths      openAPIPaths      `json:"paths"`
	Components openAPIComponents `json:"components,omitempty"`
}

type openAPIInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type openAPIPaths map[string]openAPIPathItem

type openAPIPathItem map[string]openAPIOperation

type openAPIOperation struct {
	OperationID string                     `json:"operationId"`
	Summary     string                     `json:"summary,omitempty"`
	Description string                     `json:"description,omitempty"`
	Tags        []string                   `json:"tags,omitempty"`
	Deprecated  bool                       `json:"deprecated,omitempty"`
	Parameters  []openAPIParameter         `json:"parameters,omitempty"`
	RequestBody *openAPIRequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]openAPIResponse `json:"responses"`
}

type openAPIParameter struct {
	Name        string                      `json:"name"`
	In          string                      `json:"in"`
	Description string                      `json:"description,omitempty"`
	Required    bool                        `json:"required,omitempty"`
	Content     map[string]openAPIMediaType `json:"content,omitempty"`
}

type openAPIRequestBody struct {
	Required bool                        `json:"required"`
	Content  map[string]openAPIMediaType `json:"content"`
}

type openAPIResponse struct {
	Description string                      `json:"description"`
	Content     map[string]openAPIMediaType `json:"content,omitempty"`
}

type openAPIMediaType struct {
	Schema map[string]any `json:"schema"`
}

type openAPIComponents struct {
	Schemas map[string]map[string]any `json:"schemas,omitempty"`
}

type openAPIGenerator struct {
	types       map[string]typeDeclaration
	schemaNames map[string]string
	usedNames   map[string]int
	schemas     map[string]map[string]any
	building    map[string]bool
}

func generateOpenAPI(procedures []procedure, types []typeDeclaration) ([]byte, error) {
	generator := newOpenAPIGenerator(types)

	doc := openAPIDocument{
		OpenAPI: "3.1.0",
		Info: openAPIInfo{
			Title:   "Neo API",
			Version: "1.0.0",
		},
		Paths: openAPIPaths{},
		Components: openAPIComponents{
			Schemas: generator.schemas,
		},
	}

	for _, p := range sortedProcedures(procedures) {
		path := "/" + strings.Trim(p.Key, "/")
		if path == "/" {
			continue
		}

		item := doc.Paths[path]
		if item == nil {
			item = openAPIPathItem{}
			doc.Paths[path] = item
		}

		switch strings.ToLower(p.Kind) {
		case "mutation":
			item["post"] = generator.operation(p, "", true, openAPIJSONContentType)
		case "subscription":
			item["get"] = generator.operation(p, "", false, openAPINDJSONContentType)
		default:
			item["get"] = generator.operation(p, "", false, openAPIJSONContentType)
			item["post"] = generator.operation(p, "post", true, openAPIJSONContentType)
		}
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	return out, nil
}

func newOpenAPIGenerator(types []typeDeclaration) *openAPIGenerator {
	generator := &openAPIGenerator{
		types:       make(map[string]typeDeclaration, len(types)),
		schemaNames: map[string]string{},
		usedNames:   map[string]int{},
		schemas:     map[string]map[string]any{},
		building:    map[string]bool{},
	}
	for _, typ := range types {
		generator.types[typ.Name] = typ
	}
	return generator
}

func (g *openAPIGenerator) operation(
	p procedure,
	variant string,
	useRequestBody bool,
	successContentType string,
) openAPIOperation {
	inputSchema := g.schemaForTypeString(p.Input)
	outputSchema := g.schemaForTypeString(p.Output)

	op := openAPIOperation{
		OperationID: openAPIOperationID(p, variant),
		Summary:     p.Summary,
		Description: p.Description,
		Tags:        append([]string(nil), p.Tags...),
		Deprecated:  p.Deprecated,
		Responses: map[string]openAPIResponse{
			"200":     openAPISuccessResponse(p.Kind, successContentType, outputSchema),
			"default": openAPIErrorResponse(),
		},
	}

	if useRequestBody {
		op.RequestBody = &openAPIRequestBody{
			Required: true,
			Content:  openAPIMedia(openAPIRequestEnvelope(inputSchema), openAPIJSONContentType),
		}
		return op
	}

	op.Parameters = []openAPIParameter{
		{
			Name:        "input",
			In:          "query",
			Description: "JSON-encoded procedure input.",
			Content:     openAPIMedia(inputSchema, openAPIJSONContentType),
		},
	}
	return op
}

func openAPISuccessResponse(kind string, contentType string, resultSchema map[string]any) openAPIResponse {
	description := "Neo procedure response."
	switch strings.ToLower(kind) {
	case "query":
		description = "Neo query response."
	case "mutation":
		description = "Neo mutation response."
	case "subscription":
		description = "Neo subscription stream."
	}

	return openAPIResponse{
		Description: description,
		Content:     openAPIMedia(openAPIResponseEnvelope(resultSchema), contentType),
	}
}

func openAPIErrorResponse() openAPIResponse {
	return openAPIResponse{
		Description: "Neo error response.",
		Content: openAPIMedia(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type": "string",
				},
				"error": map[string]any{
					"type": "string",
				},
			},
			"additionalProperties": false,
		}, openAPIJSONContentType),
	}
}

func openAPIMedia(schema map[string]any, contentTypes ...string) map[string]openAPIMediaType {
	content := make(map[string]openAPIMediaType, len(contentTypes))
	for _, contentType := range contentTypes {
		content[contentType] = openAPIMediaType{Schema: schema}
	}
	return content
}

func openAPIRequestEnvelope(inputSchema map[string]any) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": inputSchema,
		},
		"required":             []string{"input"},
		"additionalProperties": false,
	}
}

func openAPIResponseEnvelope(resultSchema map[string]any) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result": resultSchema,
		},
		"additionalProperties": false,
	}
}

func (g *openAPIGenerator) schemaForTypeString(typeName string) map[string]any {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return openAPIAnySchema()
	}

	expr, err := parser.ParseExpr(typeName)
	if err != nil {
		return g.schemaRef(typeName)
	}
	return g.schemaForExpr(expr)
}

func (g *openAPIGenerator) schemaForExpr(expr ast.Expr) map[string]any {
	switch e := expr.(type) {
	case *ast.Ident:
		return g.schemaForIdent(e.Name)
	case *ast.SelectorExpr:
		if exprString(e) == "time.Time" {
			return map[string]any{"type": "string", "format": "date-time"}
		}
		if exprString(e) == "json.RawMessage" {
			return openAPIAnySchema()
		}
		return g.schemaRef(exprString(e))
	case *ast.StarExpr:
		return openAPINullableSchema(g.schemaForExpr(e.X))
	case *ast.ArrayType:
		return map[string]any{
			"type":  "array",
			"items": g.schemaForExpr(e.Elt),
		}
	case *ast.MapType:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": g.schemaForExpr(e.Value),
		}
	case *ast.InterfaceType:
		return openAPIAnySchema()
	case *ast.StructType:
		return g.schemaForStruct(e)
	case *ast.ChanType:
		return g.schemaForExpr(e.Value)
	case *ast.FuncType:
		return openAPIAnySchema()
	case *ast.IndexExpr, *ast.IndexListExpr:
		return g.schemaRef(exprString(e))
	case *ast.ParenExpr:
		return g.schemaForExpr(e.X)
	default:
		return openAPIAnySchema()
	}
}

func (g *openAPIGenerator) schemaForIdent(name string) map[string]any {
	switch name {
	case "string":
		return map[string]any{"type": "string"}
	case "bool":
		return map[string]any{"type": "boolean"}
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return map[string]any{"type": "integer"}
	case "float32", "float64", "complex64", "complex128":
		return map[string]any{"type": "number"}
	case "byte", "rune":
		return map[string]any{"type": "integer"}
	case "any":
		return openAPIAnySchema()
	default:
		return g.schemaRef(name)
	}
}

func (g *openAPIGenerator) schemaForStruct(structType *ast.StructType) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
	if structType.Fields == nil {
		return schema
	}

	properties := schema["properties"].(map[string]any)
	var required []string

	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			continue
		}

		fieldSchema := g.schemaForExpr(field.Type)
		tagName, optional, stringEncoded, skip := jsonFieldTag(field)
		if skip {
			continue
		}
		if stringEncoded {
			fieldSchema = map[string]any{"type": "string"}
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

			properties[jsonName] = fieldSchema
			if !optional {
				required = append(required, jsonName)
			}
		}
	}

	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (g *openAPIGenerator) schemaRef(typeName string) map[string]any {
	componentName := g.componentName(typeName)
	g.ensureComponent(typeName, componentName)
	return map[string]any{"$ref": "#/components/schemas/" + componentName}
}

func (g *openAPIGenerator) ensureComponent(typeName string, componentName string) {
	if _, ok := g.schemas[componentName]; ok {
		return
	}
	if g.building[typeName] {
		return
	}

	g.schemas[componentName] = openAPIMetadataOnlySchema(typeName)

	decl, ok := g.types[typeName]
	if !ok {
		return
	}

	g.building[typeName] = true
	g.schemas[componentName] = g.schemaForExpr(decl.Type)
	g.building[typeName] = false
}

func (g *openAPIGenerator) componentName(typeName string) string {
	if name, ok := g.schemaNames[typeName]; ok {
		return name
	}

	base := openAPIComponentName(typeName)
	name := base
	if used := g.usedNames[name]; used > 0 {
		for {
			used++
			name = fmt.Sprintf("%s%d", base, used)
			if g.usedNames[name] == 0 {
				break
			}
		}
		g.usedNames[base] = used
	}
	g.usedNames[name]++
	g.schemaNames[typeName] = name
	return name
}

func openAPIComponentName(typeName string) string {
	var b strings.Builder
	b.Grow(len(typeName))
	upperNext := true

	for _, r := range typeName {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upperNext = true
			continue
		}
		if b.Len() == 0 && unicode.IsDigit(r) {
			b.WriteString("Value")
		}
		if upperNext {
			b.WriteRune(unicode.ToUpper(r))
			upperNext = false
			continue
		}
		b.WriteRune(r)
	}

	if b.Len() == 0 {
		return "Value"
	}
	return b.String()
}

func openAPIOperationID(p procedure, variant string) string {
	id := openAPISlug(p.Key)
	if id == "" {
		id = "procedure"
	}
	kind := openAPISlug(p.Kind)
	if kind == "" {
		kind = "query"
	}
	id += "_" + kind
	if variant != "" {
		id += "_" + openAPISlug(variant)
	}
	return id
}

func openAPISlug(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	lastUnderscore := false

	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if b.Len() == 0 && unicode.IsDigit(r) {
				b.WriteRune('_')
			}
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if b.Len() == 0 || lastUnderscore {
				continue
			}
			b.WriteRune('_')
			lastUnderscore = true
		}
	}

	return strings.Trim(b.String(), "_")
}

func openAPINullableSchema(schema map[string]any) map[string]any {
	return map[string]any{
		"anyOf": []map[string]any{
			schema,
			{"type": "null"},
		},
	}
}

func openAPIAnySchema() map[string]any {
	return map[string]any{}
}

func openAPIMetadataOnlySchema(typeName string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"description":          fmt.Sprintf("Metadata-only schema for %s.", typeName),
	}
}
