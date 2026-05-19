package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterpolateEnv_WithDefault(t *testing.T) {
	t.Setenv("FOO", "")
	out := InterpolateEnv([]byte(`level: ${FOO:-debug}`))
	assert.Equal(t, "level: debug", string(out))
}

func TestInterpolateEnv_FromEnv(t *testing.T) {
	t.Setenv("FOO", "info")
	out := InterpolateEnv([]byte(`level: ${FOO:-debug}`))
	assert.Equal(t, "level: info", string(out))
}

func TestInterpolateEnv_MissingNoDefault(t *testing.T) {
	out := InterpolateEnv([]byte(`x: ${UNSET_VAR_XYZ}`))
	assert.Equal(t, "x: ", string(out))
}

func TestLoadFromBytes_Minimal(t *testing.T) {
	t.Setenv("SEASONFILL_API_KEY", "secret")
	t.Setenv("SONARR_URL", "http://sonarr.local")
	t.Setenv("SONARR_KEY", "key123")

	raw := []byte(`
http:
  auth:
    enabled: true
    api_key: ${SEASONFILL_API_KEY}
sonarr_instances:
  - name: main
    url: ${SONARR_URL}
    api_key: ${SONARR_KEY}
`)

	cfg, err := LoadFromBytes(raw)
	require.NoError(t, err)
	assert.Equal(t, "sqlite", cfg.Database.Driver)
	assert.Equal(t, "secret", cfg.HTTP.Auth.APIKey)
	require.Len(t, cfg.SonarrInstances, 1)
	assert.Equal(t, "http://sonarr.local", cfg.SonarrInstances[0].URL)
}

func TestLoadFromBytes_MissingInstance(t *testing.T) {
	raw := []byte(`
http:
  auth:
    enabled: false
`)
	_, err := LoadFromBytes(raw)
	require.Error(t, err)
}

func TestValidate_PostgresWithoutDSN(t *testing.T) {
	cfg := Defaults()
	cfg.Database.Driver = "postgres"
	cfg.HTTP.Auth.Enabled = false
	cfg.SonarrInstances = []SonarrInstance{{Name: "x", URL: "u", APIKey: "k"}}
	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrPostgresDSN)
}

func TestValidate_AuthEnabledNoKey(t *testing.T) {
	cfg := Defaults()
	cfg.HTTP.Auth.Enabled = true
	cfg.HTTP.Auth.APIKey = ""
	cfg.SonarrInstances = []SonarrInstance{{Name: "x", URL: "u", APIKey: "k"}}
	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrAuthKeyRequired)
}

func TestValidate_UnknownDriver(t *testing.T) {
	cfg := Defaults()
	cfg.Database.Driver = "mongo"
	cfg.HTTP.Auth.Enabled = false
	cfg.SonarrInstances = []SonarrInstance{{Name: "x", URL: "u", APIKey: "k"}}
	err := cfg.Validate()
	assert.ErrorIs(t, err, ErrUnknownDriver)
}
