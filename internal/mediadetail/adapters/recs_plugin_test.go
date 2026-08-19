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

// fakeRecsPort is a hand-rolled RecsPort for the plugin/anti-storm tests.
type fakeRecsPort struct {
	covered, total int
	covErr         error
	syncedAt       *time.Time
	syncErr        error
	refreshCalls   int
	refreshLangs   []string
}

func (f *fakeRecsPort) Coverage(_ context.Context, _ int64, _ string) (int, int, error) {
	return f.covered, f.total, f.covErr
}
func (f *fakeRecsPort) RecsSyncedAt(_ context.Context, _ int64) (*time.Time, error) {
	return f.syncedAt, f.syncErr
}
func (f *fakeRecsPort) Refresh(_ context.Context, _ int64, lang string) error {
	f.refreshCalls++
	f.refreshLangs = append(f.refreshLangs, lang)
	return nil
}

func newRecsP(port RecsPort) mdengapp.SectionPlugin { return NewRecsPlugin(port, baseLang) }

func TestRecsPlugin_Section_And_CoverageNoop(t *testing.T) {
	p := newRecsP(&fakeRecsPort{})
	assert.Equal(t, mdengdomain.SectionRecs, p.Section())
	cov, tot, err := p.Coverage(context.Background(), movieMediaID(t, 7), "ru-RU")
	require.NoError(t, err)
	assert.Zero(t, cov)
	assert.Zero(t, tot, "Coverage is a NO-OP so the engine defers to Staleness (anti-storm)")
}

func TestRecsPlugin_BelowBar_StaleClock_Fires(t *testing.T) {
	old := time.Now().Add(-30 * 24 * time.Hour) // older than 7d recheck
	port := &fakeRecsPort{covered: 1, total: 20, syncedAt: &old}
	v, err := newRecsP(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.True(t, v.Stale, "coverage 5%% < 80%% bar AND clock older than recheck window → stale")
	assert.Equal(t, "gap_recheck", v.Reason)
}

// The anti-storm proof: coverage below the bar but the recs clock is RECENT →
// NOT stale → no RefreshRecommendations storm on every open.
func TestRecsPlugin_WithinRecheckWindow_NoStorm(t *testing.T) {
	recent := time.Now().Add(-1 * time.Hour) // inside the 7d window
	port := &fakeRecsPort{covered: 1, total: 20, syncedAt: &recent}
	v, err := newRecsP(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.False(t, v.Stale, "below bar but clock recent → within recheck window → NOT stale")
	assert.Equal(t, "within_recheck_window", v.Reason)
}

func TestRecsPlugin_CoveredAtOrAboveBar_Fresh(t *testing.T) {
	old := time.Now().Add(-30 * 24 * time.Hour)
	port := &fakeRecsPort{covered: 8, total: 10, syncedAt: &old} // 80% == bar
	v, err := newRecsP(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.False(t, v.Stale, "coverage at the 80%% bar is covered-enough → fresh")
	assert.Equal(t, "covered", v.Reason)
}

func TestRecsPlugin_NoRecs_Fresh(t *testing.T) {
	port := &fakeRecsPort{covered: 0, total: 0}
	v, err := newRecsP(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.False(t, v.Stale)
	assert.Equal(t, "no_recs", v.Reason)
}

func TestRecsPlugin_NeverSynced_BelowBar_Fires(t *testing.T) {
	port := &fakeRecsPort{covered: 0, total: 5, syncedAt: nil} // NULL clock
	v, err := newRecsP(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.NoError(t, err)
	assert.True(t, v.Stale)
	assert.Equal(t, "never_synced", v.Reason)
}

func TestRecsPlugin_BaseAndEmptyLang_Fresh(t *testing.T) {
	// Base lang must not even hit Coverage — set covErr to prove short-circuit.
	port := &fakeRecsPort{covErr: errors.New("must not be called")}
	for _, lang := range []string{"", baseLang} {
		v, err := newRecsP(port).Staleness(context.Background(), movieMediaID(t, 7), lang, time.Now())
		require.NoError(t, err)
		assert.False(t, v.Stale)
		assert.Equal(t, "base_lang", v.Reason)
	}
}

func TestRecsPlugin_CoverageError_ReturnsErr_FailClosed(t *testing.T) {
	port := &fakeRecsPort{covErr: errors.New("db down")}
	_, err := newRecsP(port).Staleness(context.Background(), movieMediaID(t, 7), "ru-RU", time.Now())
	require.Error(t, err, "coverage read error propagates; engine assess() then fails CLOSED (not stale)")
}

// End-to-end anti-storm through the engine Freshener: below-bar + stale-clock →
// Refresh IS driven; below-bar + recent-clock → Refresh is NOT driven.
func TestRecsPlugin_EngineDrivesRefresh_And_NoStorm(t *testing.T) {
	registry := mdengapp.NewSectionRegistry()
	stalePort := &fakeRecsPort{covered: 1, total: 20, syncedAt: new(time.Now().Add(-30 * 24 * time.Hour))}
	registry.Register(mdengdomain.MediaTypeMovie, NewRecsPlugin(stalePort, baseLang))
	fr := mdengapp.NewFreshener(registry, time.Second, time.Now, nil)

	res := fr.EnsureFresh(context.Background(), movieMediaID(t, 7), "ru-RU")
	assert.True(t, res.Refreshed, "stale recs → engine drives RefreshRecommendations")
	assert.Equal(t, 1, stalePort.refreshCalls)
	assert.Equal(t, []string{"ru-RU"}, stalePort.refreshLangs)

	// Fresh (recent clock) registry: no refresh.
	registry2 := mdengapp.NewSectionRegistry()
	freshPort := &fakeRecsPort{covered: 1, total: 20, syncedAt: new(time.Now().Add(-time.Hour))}
	registry2.Register(mdengdomain.MediaTypeMovie, NewRecsPlugin(freshPort, baseLang))
	fr2 := mdengapp.NewFreshener(registry2, time.Second, time.Now, nil)
	res2 := fr2.EnsureFresh(context.Background(), movieMediaID(t, 7), "ru-RU")
	assert.True(t, res2.Fresh, "within recheck window → engine sees Fresh")
	assert.Zero(t, freshPort.refreshCalls, "NO RefreshRecommendations storm within the recheck window")
}

// F-04 registry parity + series-dormancy: the recs plugin is registered under BOTH
// types, but nothing drives the engine with a SERIES MediaID at runtime. Driving with
// a MOVIE MediaID must NOT touch the series port (no double-drive of series_texts).
func TestRecsPlugin_RegisteredForBothTypes_SeriesArmDormant(t *testing.T) {
	registry := mdengapp.NewSectionRegistry()
	moviePort := &fakeRecsPort{covered: 1, total: 20, syncedAt: new(time.Now().Add(-30 * 24 * time.Hour))}
	seriesPort := &fakeRecsPort{covered: 1, total: 20, syncedAt: new(time.Now().Add(-30 * 24 * time.Hour))}
	registry.Register(mdengdomain.MediaTypeMovie, NewRecsPlugin(moviePort, baseLang))
	registry.Register(mdengdomain.MediaTypeSeries, NewRecsPlugin(seriesPort, baseLang))

	assertHasRecs := func(mt mdengdomain.MediaType) {
		var found bool
		for _, p := range registry.For(mt) {
			if p.Section() == mdengdomain.SectionRecs {
				found = true
			}
		}
		assert.Truef(t, found, "recs plugin registered for %s", mt.String())
	}
	assertHasRecs(mdengdomain.MediaTypeMovie)
	assertHasRecs(mdengdomain.MediaTypeSeries)

	// Driving a MOVIE id fires ONLY the movie port; the series port stays untouched.
	fr := mdengapp.NewFreshener(registry, time.Second, time.Now, nil)
	fr.EnsureFresh(context.Background(), movieMediaID(t, 7), "ru-RU")
	assert.Equal(t, 1, moviePort.refreshCalls, "movie MediaID drives ONLY the movie recs port")
	assert.Zero(t, seriesPort.refreshCalls, "series recs port is dormant — no series_texts double-drive (F-04)")
}
