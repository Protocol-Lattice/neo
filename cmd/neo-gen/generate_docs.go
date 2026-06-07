package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

func generateDocs(procedures []procedure) ([]byte, error) {
	procedures = sortedProcedures(procedures)

	var b bytes.Buffer
	b.WriteString("# Neo API\n\n")
	if len(procedures) == 0 {
		b.WriteString("No procedures discovered.\n")
		return b.Bytes(), nil
	}

	for _, p := range procedures {
		fmt.Fprintf(&b, "## `%s`\n\n", p.Key)
		if p.Summary != "" {
			fmt.Fprintf(&b, "%s\n\n", p.Summary)
		}
		if p.Deprecated {
			b.WriteString("> Deprecated.\n\n")
		}
		fmt.Fprintf(&b, "- Kind: `%s`\n", p.Kind)
		fmt.Fprintf(&b, "- Input: `%s`\n", p.Input)
		fmt.Fprintf(&b, "- Output: `%s`\n", p.Output)
		if len(p.Tags) > 0 {
			fmt.Fprintf(&b, "- Tags: `%s`\n", strings.Join(p.Tags, "`, `"))
		}
		b.WriteString("\n")
		if p.Description != "" {
			fmt.Fprintf(&b, "%s\n\n", p.Description)
		}
	}

	return b.Bytes(), nil
}

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

func generateSchema(procedures []procedure) ([]byte, error) {
	procedures = sortedProcedures(procedures)

	doc := schemaDocument{
		Schema:     "https://protocol-lattice.github.io/neo/schema/v1",
		Procedures: make([]schemaProcedure, 0, len(procedures)),
	}
	for _, p := range procedures {
		doc.Procedures = append(doc.Procedures, schemaProcedure{
			Key:         p.Key,
			Kind:        p.Kind,
			Input:       p.Input,
			Output:      p.Output,
			Summary:     p.Summary,
			Description: p.Description,
			Tags:        p.Tags,
			Deprecated:  p.Deprecated,
		})
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	return out, nil
}

func sortedProcedures(procedures []procedure) []procedure {
	out := append([]procedure(nil), procedures...)
	slices.SortFunc(out, func(a, b procedure) int {
		return cmp.Compare(a.Key, b.Key)
	})
	return out
}
