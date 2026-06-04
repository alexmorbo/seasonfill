package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractStringSlice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		claim map[string]any
		path  string
		want  []string
	}{
		{"top-level []any", map[string]any{"groups": []any{"a", "b"}}, "groups", []string{"a", "b"}},
		{"top-level []string", map[string]any{"groups": []string{"a"}}, "groups", []string{"a"}},
		{"nested keycloak", map[string]any{"realm_access": map[string]any{"roles": []any{"admin"}}}, "realm_access.roles", []string{"admin"}},
		{"scalar terminal", map[string]any{"x": "scalar"}, "x", nil},
		{"missing path", map[string]any{"a": map[string]any{}}, "a.b.c", nil},
		{"non-map intermediate", map[string]any{"a": "scalar"}, "a.b", nil},
		{"empty path", map[string]any{"a": []any{"x"}}, "", nil},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := extractStringSlice(c.claim, c.path)
			if c.want == nil {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, c.want, got)
		})
	}
}

func TestGroupACLAllows(t *testing.T) {
	t.Parallel()
	assert.True(t, groupACLAllows(map[string]any{}, "groups", nil))
	assert.True(t, groupACLAllows(
		map[string]any{"realm_access": map[string]any{"roles": []any{"admin"}}},
		"realm_access.roles", []string{"admin"}))
	assert.True(t, groupACLAllows(
		map[string]any{"groups": []any{"admin"}}, "groups", []string{"admin"}))
	assert.False(t, groupACLAllows(map[string]any{}, "groups", []string{"admin"}))
	assert.False(t, groupACLAllows(
		map[string]any{"groups": []any{"viewer"}}, "groups", []string{"admin"}))
}
