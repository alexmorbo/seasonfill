package enrichment

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsStale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name     string
		syncedAt *time.Time
		ttl      time.Duration
		want     bool
	}{
		{
			name:     "nil synced_at not stale",
			syncedAt: nil,
			ttl:      day,
			want:     false,
		},
		{
			name:     "fresh sync",
			syncedAt: new(now.Add(-1 * time.Hour)),
			ttl:      day,
			want:     false,
		},
		{
			name:     "within TTL not stale",
			syncedAt: new(now.Add(-12 * time.Hour)),
			ttl:      day,
			want:     false,
		},
		{
			name:     "between 1x and 2x TTL not stale",
			syncedAt: new(now.Add(-36 * time.Hour)),
			ttl:      day,
			want:     false,
		},
		{
			name:     "older than 2x TTL is stale",
			syncedAt: new(now.Add(-49 * time.Hour)),
			ttl:      day,
			want:     true,
		},
		{
			name:     "ttl=0 disables rule",
			syncedAt: new(now.Add(-100 * day)),
			ttl:      0,
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsStale(tc.syncedAt, tc.ttl, now)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDegraded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	ttls := map[Source]time.Duration{
		SourceTMDBSeries: day,
		SourceTMDBSeason: day,
		SourceTMDBPerson: 30 * day,
		SourceOMDb:       day,
	}
	fresh := new(now.Add(-1 * time.Hour))
	stale := new(now.Add(-72 * time.Hour))

	liveErr := &EnrichmentError{
		EntityType:  EntityTypeSeries,
		EntityID:    1,
		Source:      SourceTMDBSeries,
		LastError:   "boom",
		Attempts:    1,
		FirstSeenAt: now.Add(-1 * time.Hour),
		LastSeenAt:  now.Add(-30 * time.Minute),
	}

	cases := []struct {
		name string
		in   DegradedInput
		want []Source
	}{
		{
			name: "all fresh + both reachable",
			in: DegradedInput{
				SyncedAt: map[Source]*time.Time{
					SourceTMDBSeries: fresh,
					SourceOMDb:       fresh,
				},
				Errors:          map[Source]*EnrichmentError{},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: nil,
		},
		{
			name: "TMDB never synced",
			in: DegradedInput{
				SyncedAt: map[Source]*time.Time{
					SourceTMDBSeries: nil,
					SourceOMDb:       fresh,
				},
				Errors:          map[Source]*EnrichmentError{},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: []Source{SourceTMDBSeries},
		},
		{
			name: "TMDB live error",
			in: DegradedInput{
				SyncedAt: map[Source]*time.Time{
					SourceTMDBSeries: fresh,
				},
				Errors: map[Source]*EnrichmentError{
					SourceTMDBSeries: liveErr,
				},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: []Source{SourceTMDBSeries},
		},
		{
			name: "OMDb stale (>2xTTL)",
			in: DegradedInput{
				SyncedAt: map[Source]*time.Time{
					SourceTMDBSeries: fresh,
					SourceOMDb:       stale,
				},
				Errors:          map[Source]*EnrichmentError{},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: []Source{SourceOMDb},
		},
		{
			name: "Sonarr unreachable",
			in: DegradedInput{
				SyncedAt:        map[Source]*time.Time{},
				Errors:          map[Source]*EnrichmentError{},
				TTLs:            ttls,
				SonarrReachable: false,
				QbitReachable:   true,
			},
			want: []Source{SourceSonarr},
		},
		{
			name: "qBit unreachable",
			in: DegradedInput{
				SyncedAt:        map[Source]*time.Time{},
				Errors:          map[Source]*EnrichmentError{},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   false,
			},
			want: []Source{SourceQbit},
		},
		{
			name: "mixed: TMDB error + qBit unreachable",
			in: DegradedInput{
				SyncedAt: map[Source]*time.Time{
					SourceTMDBSeries: fresh,
				},
				Errors: map[Source]*EnrichmentError{
					SourceTMDBSeries: liveErr,
				},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   false,
			},
			// Canonical order: TMDB series before qBit.
			want: []Source{SourceTMDBSeries, SourceQbit},
		},
		{
			name: "ordering: all sources degraded",
			in: DegradedInput{
				SyncedAt: map[Source]*time.Time{
					SourceTMDBSeries: nil,
					SourceTMDBSeason: nil,
					SourceTMDBPerson: nil,
					SourceOMDb:       nil,
				},
				Errors:          map[Source]*EnrichmentError{},
				TTLs:            ttls,
				SonarrReachable: false,
				QbitReachable:   false,
			},
			want: []Source{
				SourceTMDBSeries, SourceTMDBSeason,
				SourceTMDBPerson, SourceOMDb,
				SourceSonarr, SourceQbit,
			},
		},
		{
			name: "source not declared is skipped",
			in: DegradedInput{
				SyncedAt:        map[Source]*time.Time{},
				Errors:          map[Source]*EnrichmentError{},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Degraded(tc.in, now)
			if len(tc.want) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

// TestDegraded_LegacyLogsShape exercises the deprecated `Logs` map
// branch of DegradedInput, kept in the 464a kernel so the composer
// pre-rewrite path keeps compiling. Deleted alongside the
// `DegradedInput.Logs` field in 464b.
func TestDegraded_LegacyLogsShape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	ttls := map[Source]time.Duration{
		SourceTMDBSeries: day,
		SourceOMDb:       day,
	}
	fresh := new(now.Add(-1 * time.Hour))

	cases := []struct {
		name string
		in   DegradedInput
		want []Source
	}{
		{
			name: "legacy Logs: TMDB error outcome",
			in: DegradedInput{
				Logs: map[Source]*SyncLog{
					SourceTMDBSeries: {SyncedAt: fresh, Outcome: OutcomeError},
				},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: []Source{SourceTMDBSeries},
		},
		{
			name: "legacy Logs: pending outcome NOT degraded by rule 2",
			in: DegradedInput{
				Logs: map[Source]*SyncLog{
					SourceTMDBSeries: {SyncedAt: fresh, Outcome: OutcomePending},
				},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Degraded(tc.in, now)
			if len(tc.want) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
