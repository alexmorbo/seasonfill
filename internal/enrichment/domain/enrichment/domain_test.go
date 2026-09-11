package enrichment

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSource_IsValid pins the journalled-source gate. IsValid() guards
// PERSISTENCE (enrichment_errors_repository.go:49 rejects an unknown source), so
// a journalled source that fails it can never be written — the ADR-0025 Р4-bis
// invariant. SourceSonarr / SourceQbit are the two declared known-exceptions:
// they are live-reachability flags surfaced through degraded[], never persisted
// (degraded.go:5-9), and they MUST keep failing.
func TestSource_IsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   Source
		want bool
	}{
		{"tmdb_series", SourceTMDBSeries, true},
		{"tmdb_season", SourceTMDBSeason, true},
		{"tmdb_person", SourceTMDBPerson, true},
		{"omdb", SourceOMDb, true},
		{"tvdb_resolve", SourceTVDBResolve, true},
		{"tmdb_movie (ADR-0025 Ф1)", SourceTMDBMovie, true},
		{"raw literal tmdb_movie", Source("tmdb_movie"), true},
		{"sonarr is a declared known-exception", SourceSonarr, false},
		{"qbit is a declared known-exception", SourceQbit, false},
		{"empty", Source(""), false},
		{"unknown", Source("garbage"), false},
		{"case-sensitive", Source("TMDB_MOVIE"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.in.IsValid())
		})
	}
}

// TestEntityType_IsValid pins the entity_type gate shared by enrichment_errors
// and external_ids.
func TestEntityType_IsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   EntityType
		want bool
	}{
		{"series", EntityTypeSeries, true},
		{"season", EntityTypeSeason, true},
		{"person", EntityTypePerson, true},
		{"episode", EntityTypeEpisode, true},
		{"movie (ADR-0025 Ф1)", EntityTypeMovie, true},
		{"raw literal movie", EntityType("movie"), true},
		{"empty", EntityType(""), false},
		{"unknown", EntityType("garbage"), false},
		{"case-sensitive", EntityType("Movie"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.in.IsValid())
		})
	}
}

// TestMovieEnumsAreDistinctFromSeries guards the copy-paste failure mode this
// whole ADR is about: a movie constant that silently carries the series value
// would write movie failures into the series ledger and the picker gates of both
// verticals would start lying.
func TestMovieEnumsAreDistinctFromSeries(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t, SourceTMDBSeries, SourceTMDBMovie)
	assert.NotEqual(t, EntityTypeSeries, EntityTypeMovie)
	assert.Equal(t, "tmdb_movie", string(SourceTMDBMovie))
	assert.Equal(t, "movie", string(EntityTypeMovie))
}
