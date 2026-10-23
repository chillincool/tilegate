package claims

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParsePath(t *testing.T) {
	tests := map[string]Path{
		"groups":                         {"groups"},
		"resource_access.tilegate.roles": {"resource_access", "tilegate", "roles"},
		`https://example\.com/groups`:    {"https://example.com/groups"},
		`a\.b.c`:                         {"a.b", "c"},
	}
	for in, want := range tests {
		if got := ParsePath(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParsePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookup(t *testing.T) {
	var c map[string]any
	raw := `{
		"groups": ["admins", "family"],
		"scope": "openid email profile",
		"realm_access": {"roles": ["offline_access", "media"]},
		"resource_access": {"tilegate": {"roles": ["viewer"]}},
		"https://example.com/groups": ["x"],
		"radarr_roles": "access",
		"mixed": ["a", 1, null, ""]
	}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	tests := map[string][]string{
		"groups":                         {"admins", "family"},
		"scope":                          {"openid", "email", "profile"},
		"realm_access.roles":             {"offline_access", "media"},
		"resource_access.tilegate.roles": {"viewer"},
		`https://example\.com/groups`:    {"x"},
		"radarr_roles":                   {"access"},
		"mixed":                          {"a"},
		"missing":                        nil,
		"groups.nested":                  nil,
		"realm_access":                   nil,
	}
	for path, want := range tests {
		got := Lookup(c, ParsePath(path))
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Lookup(%q) = %q, want %q", path, got, want)
		}
	}
}
