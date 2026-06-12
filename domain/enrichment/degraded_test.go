package enrichment

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func tPtrTime(t time.Time) *time.Time { return &t }

func TestIsStale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name  string
		entry SyncLog
		ttl   time.Duration
		want  bool
	}{
		{
			name:  "nil synced_at not stale",
			entry: SyncLog{SyncedAt: nil},
			ttl:   day,
			want:  false,
		},
		{
			name:  "fresh sync",
			entry: SyncLog{SyncedAt: tPtrTime(now.Add(-1 * time.Hour))},
			ttl:   day,
			want:  false,
		},
		{
			name:  "within TTL not stale",
			entry: SyncLog{SyncedAt: tPtrTime(now.Add(-12 * time.Hour))},
			ttl:   day,
			want:  false,
		},
		{
			name:  "between 1x and 2x TTL not stale",
			entry: SyncLog{SyncedAt: tPtrTime(now.Add(-36 * time.Hour))},
			ttl:   day,
			want:  false,
		},
		{
			name:  "older than 2x TTL is stale",
			entry: SyncLog{SyncedAt: tPtrTime(now.Add(-49 * time.Hour))},
			ttl:   day,
			want:  true,
		},
		{
			name:  "ttl=0 disables rule",
			entry: SyncLog{SyncedAt: tPtrTime(now.Add(-100 * day))},
			ttl:   0,
			want:  false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsStale(tc.entry, tc.ttl, now)
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
	fresh := tPtrTime(now.Add(-1 * time.Hour))
	stale := tPtrTime(now.Add(-72 * time.Hour))

	cases := []struct {
		name string
		in   DegradedInput
		want []Source
	}{
		{
			name: "all fresh + both reachable",
			in: DegradedInput{
				Logs: map[Source]*SyncLog{
					SourceTMDBSeries: {SyncedAt: fresh, Outcome: OutcomeOK},
					SourceOMDb:       {SyncedAt: fresh, Outcome: OutcomeOK},
				},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: nil,
		},
		{
			name: "TMDB never synced",
			in: DegradedInput{
				Logs: map[Source]*SyncLog{
					SourceTMDBSeries: nil,
					SourceOMDb:       {SyncedAt: fresh, Outcome: OutcomeOK},
				},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: []Source{SourceTMDBSeries},
		},
		{
			name: "TMDB error outcome",
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
			name: "OMDb stale (>2xTTL)",
			in: DegradedInput{
				Logs: map[Source]*SyncLog{
					SourceTMDBSeries: {SyncedAt: fresh, Outcome: OutcomeOK},
					SourceOMDb:       {SyncedAt: stale, Outcome: OutcomeOK},
				},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: []Source{SourceOMDb},
		},
		{
			name: "Sonarr unreachable",
			in: DegradedInput{
				Logs:            map[Source]*SyncLog{},
				TTLs:            ttls,
				SonarrReachable: false,
				QbitReachable:   true,
			},
			want: []Source{SourceSonarr},
		},
		{
			name: "qBit unreachable",
			in: DegradedInput{
				Logs:            map[Source]*SyncLog{},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   false,
			},
			want: []Source{SourceQbit},
		},
		{
			name: "mixed: TMDB error + qBit unreachable",
			in: DegradedInput{
				Logs: map[Source]*SyncLog{
					SourceTMDBSeries: {SyncedAt: fresh, Outcome: OutcomeError},
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
				Logs: map[Source]*SyncLog{
					SourceTMDBSeries: nil,
					SourceTMDBSeason: nil,
					SourceTMDBPerson: nil,
					SourceOMDb:       nil,
				},
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
			name: "source not declared in Logs is skipped",
			in: DegradedInput{
				Logs:            map[Source]*SyncLog{},
				TTLs:            ttls,
				SonarrReachable: true,
				QbitReachable:   true,
			},
			want: nil,
		},
		{
			name: "pending outcome NOT degraded by rule 2",
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
		tc := tc
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
