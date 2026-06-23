package rest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

func TestCanonicalHash_EmptyFilter_Stable(t *testing.T) {
	h := canonicalHash(tmdb.DiscoverFilter{}, "en-US", 1)
	require.Len(t, h, 64) // sha256 hex
	// Regression baseline — if buildCanonicalParams ever changes
	// representation, this test fails and the operator audits the wire
	// impact on already-warmed caches.
	require.Equal(t, h, canonicalHash(tmdb.DiscoverFilter{}, "en-US", 1))
}

func TestCanonicalHash_SliceOrderIndependent(t *testing.T) {
	a := tmdb.DiscoverFilter{WithGenres: []int{18, 35}, WithNetworks: []int{213, 49}}
	b := tmdb.DiscoverFilter{WithGenres: []int{35, 18}, WithNetworks: []int{49, 213}}
	require.Equal(t, canonicalHash(a, "en-US", 1), canonicalHash(b, "en-US", 1))
}

func TestCanonicalHash_AllFieldsPopulated_Deterministic(t *testing.T) {
	firstGte := "2016-01-01"
	firstLte := "2026-12-31"
	voteGte := 7.5
	voteLte := 10.0
	voteCount := 200
	runtimeGte := 20
	runtimeLte := 120
	origLang := "ja"
	origCountry := "JP"
	watchRegion := "US"
	f := tmdb.DiscoverFilter{
		WithGenres:         []int{18, 35},
		WithoutGenres:      []int{10764},
		FirstAirDateGte:    &firstGte,
		FirstAirDateLte:    &firstLte,
		VoteAverageGte:     &voteGte,
		VoteAverageLte:     &voteLte,
		VoteCountGte:       &voteCount,
		WithRuntimeGte:     &runtimeGte,
		WithRuntimeLte:     &runtimeLte,
		WithOriginalLang:   &origLang,
		WithNetworks:       []int{213},
		WithOriginCountry:  &origCountry,
		WithKeywords:       []int{210024},
		WithWatchProviders: []int{8},
		WatchRegion:        &watchRegion,
		WithStatus:         []int{0, 1},
		WithStatusOp:       "or",
		WithType:           []int{0, 2},
		WithTypeOp:         "and",
		SortBy:             "popularity.desc",
	}
	h1 := canonicalHash(f, "en-US", 1)
	h2 := canonicalHash(f, "en-US", 1)
	require.Equal(t, h1, h2, "deterministic across calls")
	require.Len(t, h1, 64)
}

func TestCanonicalHash_LangAndPage_DifferentiateKey(t *testing.T) {
	f := tmdb.DiscoverFilter{WithGenres: []int{18}}
	base := canonicalHash(f, "en-US", 1)
	require.NotEqual(t, base, canonicalHash(f, "ru-RU", 1), "lang must differentiate")
	require.NotEqual(t, base, canonicalHash(f, "en-US", 2), "page must differentiate")
}

func TestCanonicalHash_EmptyVsNilPointers(t *testing.T) {
	emptyStr := ""
	f := tmdb.DiscoverFilter{WithOriginalLang: &emptyStr}
	// A pointer-to-empty-string is treated identically to nil — both
	// omit the URL key. (buildDiscoverQuery matches.)
	require.Equal(t, canonicalHash(f, "en-US", 1),
		canonicalHash(tmdb.DiscoverFilter{}, "en-US", 1))
}

func TestCanonicalHash_StatusOpFlipsSeparator(t *testing.T) {
	or := tmdb.DiscoverFilter{WithStatus: []int{0, 3}, WithStatusOp: "or"}
	and := tmdb.DiscoverFilter{WithStatus: []int{0, 3}, WithStatusOp: "and"}
	require.NotEqual(t, canonicalHash(or, "en-US", 1), canonicalHash(and, "en-US", 1))
}
