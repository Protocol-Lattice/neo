package main

import (
	"slices"
	"strings"
	"unicode"
)

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
	slices.Sort(keys)
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
	b.Grow(len(s))
	upperNext := true
	for _, r := range s {
		switch r {
		case '.', '_', '-', '/':
			upperNext = true
			continue
		}

		if upperNext {
			b.WriteRune(unicode.ToUpper(r))
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
