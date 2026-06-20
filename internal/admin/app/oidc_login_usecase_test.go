package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGroupACLAllows_EmptyAllowsAll(t *testing.T) {
	t.Parallel()
	claims := map[string]any{"groups": []any{"users"}}
	assert.True(t, groupACLAllows(claims, "groups", nil))
	assert.True(t, groupACLAllows(claims, "groups", []string{}))
}

func TestGroupACLAllows_NoGroupsClaimDenied(t *testing.T) {
	t.Parallel()
	claims := map[string]any{}
	assert.False(t, groupACLAllows(claims, "groups", []string{"admins"}))
}

func TestGroupACLAllows_IntersectionMatches(t *testing.T) {
	t.Parallel()
	claims := map[string]any{"groups": []any{"users", "admins"}}
	assert.True(t, groupACLAllows(claims, "groups", []string{"admins"}))
	assert.True(t, groupACLAllows(claims, "groups", []string{"admins", "ops"}))
}

func TestGroupACLAllows_NoIntersectionDenied(t *testing.T) {
	t.Parallel()
	claims := map[string]any{"groups": []any{"users"}}
	assert.False(t, groupACLAllows(claims, "groups", []string{"admins"}))
}

func TestGroupACLAllows_StringClaim(t *testing.T) {
	// Some providers emit `groups` as a comma-separated string when the
	// member has exactly one group. stringSliceFromClaim handles this
	// via the case string branch.
	t.Parallel()
	claims := map[string]any{"groups": "admins"}
	assert.True(t, groupACLAllows(claims, "groups", []string{"admins"}))
}

func TestStringClaim_Present(t *testing.T) {
	t.Parallel()
	c := map[string]any{"preferred_username": "alice"}
	assert.Equal(t, "alice", stringClaim(c, "preferred_username"))
}

func TestStringClaim_MissingReturnsEmpty(t *testing.T) {
	t.Parallel()
	c := map[string]any{}
	assert.Empty(t, stringClaim(c, "preferred_username"))
}

func TestStringClaim_NonStringTypeReturnsEmpty(t *testing.T) {
	t.Parallel()
	c := map[string]any{"preferred_username": 42}
	assert.Empty(t, stringClaim(c, "preferred_username"))
}

func TestStringSliceFromClaim_AnySlice(t *testing.T) {
	t.Parallel()
	got := stringSliceFromClaim([]any{"a", "b"})
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestStringSliceFromClaim_StringSlice(t *testing.T) {
	t.Parallel()
	got := stringSliceFromClaim([]string{"a", "b"})
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestStringSliceFromClaim_SingleString(t *testing.T) {
	t.Parallel()
	got := stringSliceFromClaim("admins")
	assert.Equal(t, []string{"admins"}, got)
}

func TestStringSliceFromClaim_EmptyString(t *testing.T) {
	t.Parallel()
	got := stringSliceFromClaim("")
	assert.Nil(t, got)
}

func TestSafeReturnTo_DefaultsToRoot(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/", SafeReturnTo(""))
	assert.Equal(t, "/", SafeReturnTo("https://evil.example.com/"))
	assert.Equal(t, "/", SafeReturnTo("//evil.example.com/path"))
}

func TestSafeReturnTo_PreservesSafe(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/instances", SafeReturnTo("/instances"))
	assert.Equal(t, "/settings?tab=security", SafeReturnTo("/settings?tab=security"))
}

func TestSafeReturnTo_RejectsRelative(t *testing.T) {
	t.Parallel()
	// No leading slash => not same-origin
	assert.Equal(t, "/", SafeReturnTo("instances"))
}

func TestRandomToken_NonEmpty(t *testing.T) {
	t.Parallel()
	tok, err := randomToken(32)
	assert.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Greater(t, len(tok), 16)
}

func TestRandomToken_Unique(t *testing.T) {
	t.Parallel()
	a, _ := randomToken(32)
	b, _ := randomToken(32)
	assert.NotEqual(t, a, b)
}
