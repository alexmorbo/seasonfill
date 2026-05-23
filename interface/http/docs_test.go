package http

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// isExempt prefix-matches paths in server.go deliberately kept out
// of the spec. Prefix-match (not exact) so that any future webhook
// sub-route — e.g. /api/v1/webhook/sonarr/:id/test — is exempt by
// construction; reviewers don't have to remember to extend an
// allowlist when new webhook variants land.
func isExempt(path string) bool {
	if strings.HasPrefix(path, "/api/v1/webhook/") {
		return true
	}
	return path == "/metrics"
}

var ginParamRE = regexp.MustCompile(`:([^/]+)`)

func TestSwaggerYAML_CoversAllRoutes(t *testing.T) {
	t.Parallel()

	yamlPath := locateSwaggerYAML(t)
	raw, err := os.ReadFile(yamlPath)
	require.NoError(t, err)
	var spec struct {
		Paths map[string]any `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &spec))
	require.NotEmpty(t, spec.Paths, "swagger.yaml has no paths section")

	srv := newServerForTest(t, "test-key")
	routes := srv.engine.Routes()
	require.NotEmpty(t, routes, "engine has no routes")

	missing := []string{}
	for _, r := range routes {
		if isExempt(r.Path) {
			continue
		}
		specPath := ginParamRE.ReplaceAllString(r.Path, "{$1}")
		base := strings.TrimSuffix(specPath, "/")
		needle := strings.TrimPrefix(base, "/api/v1")
		if needle == "" {
			needle = "/"
		}
		if _, ok := spec.Paths[needle]; ok {
			continue
		}
		if _, ok := spec.Paths[base]; ok {
			continue
		}
		missing = append(missing, r.Method+" "+r.Path+" (looked for "+needle+")")
	}
	assert.Empty(t, missing,
		"routes in server.go absent from docs/swagger.yaml:\n  %s\n\nrun `make openapi`.",
		strings.Join(missing, "\n  "),
	)
}

func locateSwaggerYAML(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for dir := cwd; dir != "/"; dir = filepath.Dir(dir) {
		p := filepath.Join(dir, "docs", "swagger.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatalf("docs/swagger.yaml not found ascending from %s", cwd)
	return ""
}
