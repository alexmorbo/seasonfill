package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mdengapp "github.com/alexmorbo/seasonfill/internal/mediadetail/app"
	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
)

// fakeCastPort is a hand-rolled CastPort for the plugin/anti-storm tests.
type fakeCastPort struct {
	covered, total int
	covErr         error
	syncedAt       *time.Time
	syncErr        error
	refreshCalls   int
	refreshLangs   []string
}

func (f *fakeCastPort) Coverage(_ context.Context, _ int64, _ string) (int, int, error) {
	return f.covered, f.total, f.covErr
}
func (f *fakeCastPort) CastSyncedAt(_ context.Context, _ int64) (*time.Time, error) {
	return f.syncedAt, f.syncErr
}
func (f *fakeCastPort) Refresh(_ context.Context, _ int64, lang string) error {
	f.refreshCalls++
	f.refreshLangs = append(f.refreshLangs, lang)
	return nil
}

const baseLang = "en-US"

func newPlugin(port CastPort) mdengapp.SectionPlugin { return NewCastPlugin(port, baseLang) }

func TestCastPlugin_Section_And_CoverageNoop(t *testing.T) {
	p := newPlugin(&fakeCastPort{})
	assert.Equal(t, mdengdomain.SectionCast, p.Section())
	cov, tot, err := p.Coverage(context.Background(), movieMediaID(t, 7), "ru-RU")
	require.NoError(t, err)
	assert.Zero(t, cov)
	assert.Zero(t, tot, "Coverage is a NO-OP so the engine defers to Staleness (anti-storm)")
}

func TestCastPlugin_BelowBar_StaleClock_Fires(t *testing.T) {
	old := time.Now().Add(-30 * 24 * time.Hour) // older than 7d recheck
	port := &fakeCastPort{covered: 1, total: 10, syncedAt: &old}
	v, err := newPlugin(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.True(t, v.Stale, "coverage 10%% < 80%% bar AND clock older than recheck window → stale")
	assert.Equal(t, "gap_recheck", v.Reason)
}

// The anti-storm proof: coverage is below the bar but the cast clock is RECENT →
// NOT stale → no RefreshCast storm on every open.
func TestCastPlugin_WithinRecheckWindow_NoStorm(t *testing.T) {
	recent := time.Now().Add(-1 * time.Hour) // inside the 7d window
	port := &fakeCastPort{covered: 1, total: 10, syncedAt: &recent}
	v, err := newPlugin(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.False(t, v.Stale, "below bar but clock recent → within recheck window → NOT stale")
	assert.Equal(t, "within_recheck_window", v.Reason)
}

func TestCastPlugin_CoveredAtOrAboveBar_Fresh(t *testing.T) {
	old := time.Now().Add(-30 * 24 * time.Hour)
	port := &fakeCastPort{covered: 8, total: 10, syncedAt: &old} // 80% == bar
	v, err := newPlugin(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.False(t, v.Stale, "coverage at the 80%% bar is covered-enough → fresh")
	assert.Equal(t, "covered", v.Reason)
}

func TestCastPlugin_NoCast_Fresh(t *testing.T) {
	port := &fakeCastPort{covered: 0, total: 0}
	v, err := newPlugin(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.False(t, v.Stale)
	assert.Equal(t, "no_cast", v.Reason)
}

func TestCastPlugin_NeverSynced_BelowBar_Fires(t *testing.T) {
	port := &fakeCastPort{covered: 0, total: 5, syncedAt: nil} // NULL clock
	v, err := newPlugin(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.True(t, v.Stale)
	assert.Equal(t, "never_synced", v.Reason)
}

func TestCastPlugin_BaseAndEmptyLang_Fresh(t *testing.T) {
	// Base lang must not even hit Coverage — set covErr to prove short-circuit.
	port := &fakeCastPort{covErr: errors.New("must not be called")}
	for _, lang := range []string{"", baseLang} {
		v, err := newPlugin(port).Staleness(context.Background(), movieMediaID(t, 7), lang, time.Now())
		require.NoError(t, err)
		assert.False(t, v.Stale)
		assert.Equal(t, "base_lang", v.Reason)
	}
}

func TestCastPlugin_CoverageError_ReturnsErr_FailClosed(t *testing.T) {
	port := &fakeCastPort{covErr: errors.New("db down")}
	_, err := newPlugin(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.Error(t, err, "coverage read error propagates; engine assess() then fails CLOSED (not stale)")
}

// End-to-end anti-storm through the engine Freshener: below-bar + stale-clock →
// Refresh IS driven; below-bar + recent-clock → Refresh is NOT driven.
func TestCastPlugin_EngineDrivesRefresh_And_NoStorm(t *testing.T) {
	registry := mdengapp.NewSectionRegistry()
	stalePort := &fakeCastPort{covered: 1, total: 10, syncedAt: new(time.Now().Add(-30 * 24 * time.Hour))}
	registry.Register(mdengdomain.MediaTypeMovie, NewCastPlugin(stalePort, baseLang))
	fr := mdengapp.NewFreshener(registry, time.Second, time.Now, nil)

	res := fr.EnsureFresh(context.Background(), movieMediaID(t, 7), "ru-RU")
	assert.True(t, res.Refreshed, "stale cast → engine drives RefreshCast")
	assert.Equal(t, 1, stalePort.refreshCalls)
	assert.Equal(t, []string{"ru-RU"}, stalePort.refreshLangs)

	// Fresh (recent clock) registry: no refresh.
	registry2 := mdengapp.NewSectionRegistry()
	freshPort := &fakeCastPort{covered: 1, total: 10, syncedAt: new(time.Now().Add(-time.Hour))}
	registry2.Register(mdengdomain.MediaTypeMovie, NewCastPlugin(freshPort, baseLang))
	fr2 := mdengapp.NewFreshener(registry2, time.Second, time.Now, nil)
	res2 := fr2.EnsureFresh(context.Background(), movieMediaID(t, 7), "ru-RU")
	assert.True(t, res2.Fresh, "within recheck window → engine sees Fresh")
	assert.Zero(t, freshPort.refreshCalls, "NO RefreshCast storm within the recheck window")
}

// Registry parity: the cast plugin is registered under BOTH types; the series arm
// is present but no runtime code drives the engine with a series MediaID (F-05).
func TestCastPlugin_RegisteredForBothTypes(t *testing.T) {
	registry := mdengapp.NewSectionRegistry()
	registry.Register(mdengdomain.MediaTypeMovie, NewCastPlugin(&fakeCastPort{}, baseLang))
	registry.Register(mdengdomain.MediaTypeSeries, NewCastPlugin(&fakeCastPort{}, baseLang))

	assertHasCast := func(mt mdengdomain.MediaType) {
		var found bool
		for _, p := range registry.For(mt) {
			if p.Section() == mdengdomain.SectionCast {
				found = true
			}
		}
		assert.Truef(t, found, "cast plugin registered for %s", mt.String())
	}
	assertHasCast(mdengdomain.MediaTypeMovie)
	assertHasCast(mdengdomain.MediaTypeSeries)
}
