// Package claims extracts role/group values from decoded JWT claims using
// dotted paths such as "groups" or "resource_access.my-client.roles".
package claims

import (
	"strings"
)

// Path is a parsed claim path. Each element is one level of nesting.
type Path []string

// ParsePath splits a dotted claim path. A backslash escapes a literal dot, so
// URL-style claim names work: `https://example\.com/groups`.
func ParsePath(s string) Path {
	var (
		parts []string
		cur   strings.Builder
	)
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '.':
			cur.WriteByte('.')
			i++
		case s[i] == '.':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	parts = append(parts, cur.String())
	return parts
}

// Lookup walks the claims along the path and returns the string values found
// there. Arrays of strings, single strings and space- or comma-separated strings
// are all accepted, since IdPs disagree on how to encode lists.
func Lookup(claims map[string]any, p Path) []string {
	var cur any = claims
	for _, key := range p {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		if cur, ok = m[key]; !ok {
			return nil
		}
	}
	switch v := cur.(type) {
	case string:
		return strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' })
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}
