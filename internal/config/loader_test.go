package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadFromBytes_DryRunBareBool_GlobalAndPerInstance regression-guards B-1:
// koanf must accept bare YAML bools for `dry_run` at the top level and at the
// per-instance level. The pre-fix `DryRunPtr` struct silently failed to bind
// bare bools (it only worked for string values via UnmarshalText), which
// caused real-grab decisions to ignore per-instance overrides.
func TestLoadFromBytes_DryRunBareBool_GlobalAndPerInstance(t *testing.T) {
	t.Setenv("SEASONFILL_API_KEY", "secret")

	raw := []byte(`
http:
  auth:
    enabled: true
    api_key: ${SEASONFILL_API_KEY}

dry_run: true

sonarr_instances:
  - name: sonarr-main
    url: http://sonarr-main.local
    api_key: key-main
  - name: sonarr-4k
    url: http://sonarr-4k.local
    api_key: key-4k
    dry_run: false
  - name: sonarr-anime
    url: http://sonarr-anime.local
    api_key: key-anime
    dry_run: true
`)

	cfg, err := LoadFromBytes(raw)
	require.NoError(t, err)
	require.Len(t, cfg.SonarrInstances, 3)

	// Global default loaded.
	assert.True(t, cfg.DryRun, "global dry_run should be true")

	// Instance 0: no override -> inherits global true.
	main := cfg.SonarrInstances[0]
	assert.Nil(t, main.DryRun, "no override should leave DryRun nil")
	assert.True(t, cfg.DryRunFor(main), "main inherits global dry_run=true")

	// Instance 1: explicit false override wins.
	fourK := cfg.SonarrInstances[1]
	require.NotNil(t, fourK.DryRun, "explicit override should populate DryRun")
	assert.False(t, *fourK.DryRun)
	assert.False(t, cfg.DryRunFor(fourK), "4k overrides to real grab")

	// Instance 2: explicit true is also honored (matches global, but still bound).
	anime := cfg.SonarrInstances[2]
	require.NotNil(t, anime.DryRun)
	assert.True(t, *anime.DryRun)
	assert.True(t, cfg.DryRunFor(anime))
}

// TestLoadFromBytes_DryRunDefaultsTrue regression-guards H-2: the safe
// default must remain `true` so a freshly-pulled image does NOT issue real
// grabs without explicit opt-in. Operators set `dry_run: false` only after
// verifying decisions in logs.
func TestLoadFromBytes_DryRunDefaultsTrue(t *testing.T) {
	t.Setenv("SEASONFILL_API_KEY", "secret")

	raw := []byte(`
http:
  auth:
    enabled: true
    api_key: ${SEASONFILL_API_KEY}

sonarr_instances:
  - name: main
    url: http://sonarr.local
    api_key: key
`)

	cfg, err := LoadFromBytes(raw)
	require.NoError(t, err)
	assert.True(t, cfg.DryRun, "dry_run must default to true when omitted from YAML")
	assert.True(t, cfg.DryRunFor(cfg.SonarrInstances[0]))
}

// TestLoadFromBytes_DryRunGlobalFalseInstanceOverrideTrue covers the
// inverse rollout path: globally enabled (false), one instance reverts to
// dry-run for safety.
func TestLoadFromBytes_DryRunGlobalFalseInstanceOverrideTrue(t *testing.T) {
	t.Setenv("SEASONFILL_API_KEY", "secret")

	raw := []byte(`
http:
  auth:
    enabled: true
    api_key: ${SEASONFILL_API_KEY}

dry_run: false

sonarr_instances:
  - name: sonarr-main
    url: http://sonarr-main.local
    api_key: key-main
  - name: sonarr-experimental
    url: http://sonarr-exp.local
    api_key: key-exp
    dry_run: true
`)

	cfg, err := LoadFromBytes(raw)
	require.NoError(t, err)
	require.Len(t, cfg.SonarrInstances, 2)
	assert.False(t, cfg.DryRun)

	main := cfg.SonarrInstances[0]
	assert.Nil(t, main.DryRun)
	assert.False(t, cfg.DryRunFor(main))

	exp := cfg.SonarrInstances[1]
	require.NotNil(t, exp.DryRun)
	assert.True(t, *exp.DryRun)
	assert.True(t, cfg.DryRunFor(exp))
}
