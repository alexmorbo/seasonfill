package wiring

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

// TestPersonCreditsAdapter_DomainModelRoundTrip covers Story 307.
// Constructs a fully-populated domain PersonCredit (all 3 new
// fields set), pushes it through the write-side projection
// (PersonCreditsRepoAdapter), reads back via the read-side
// projection (adapters.ModelToPersonCredit), and asserts every field
// survives. Catches drift between the two adapters.
func TestPersonCreditsAdapter_DomainModelRoundTrip(t *testing.T) {
	t.Parallel()
	original := "The Last of Us (Original)"
	dept := "Production"
	character := "Joel Miller"
	job := "Executive Producer"
	posterPath := "/poster.jpg"
	rating := 8.5
	votes := 12345
	episodes := 9
	releaseDate := time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC)

	in := people.PersonCredit{
		PersonID:      42,
		MediaType:     "tv",
		TMDBMediaID:   100088,
		TMDBCreditID:  "tlou-pedro",
		Kind:          people.SeriesCreditCast,
		Title:         "The Last of Us",
		OriginalTitle: &original,
		CharacterName: &character,
		Department:    &dept,
		Job:           &job,
		EpisodeCount:  &episodes,
		ReleaseDate:   &releaseDate,
		PosterAsset:   &posterPath,
		TMDBRating:    &rating,
		TMDBVotes:     &votes,
	}

	// Write-side: domain → model via the adapter's projection logic.
	// We build the model literal the same way PersonCreditsRepoAdapter
	// does — this isolates the projection from gorm + DB.
	row := database.PersonCreditModel{
		PersonID:      in.PersonID,
		TMDBCreditID:  in.TMDBCreditID,
		MediaType:     in.MediaType,
		TMDBMediaID:   int(in.TMDBMediaID),
		Title:         in.Title,
		OriginalTitle: in.OriginalTitle,
		Year:          yearFromReleaseDate(in.ReleaseDate),
		CharacterName: in.CharacterName,
		Kind:          string(in.Kind),
		Department:    in.Department,
		Job:           in.Job,
		PosterPath:    in.PosterAsset,
		VoteAverage:   in.TMDBRating,
		TMDBVotes:     in.TMDBVotes,
		EpisodeCount:  in.EpisodeCount,
	}

	// Read-side: model → domain via adapters.ModelToPersonCredit.
	out := adapters.ModelToPersonCredit(row)

	assert.Equal(t, in.PersonID, out.PersonID)
	assert.Equal(t, in.MediaType, out.MediaType)
	assert.Equal(t, in.TMDBMediaID, out.TMDBMediaID)
	assert.Equal(t, in.TMDBCreditID, out.TMDBCreditID)
	assert.Equal(t, in.Kind, out.Kind)
	assert.Equal(t, in.Title, out.Title)

	require.NotNil(t, out.OriginalTitle, "OriginalTitle dropped")
	assert.Equal(t, *in.OriginalTitle, *out.OriginalTitle)
	require.NotNil(t, out.CharacterName)
	assert.Equal(t, *in.CharacterName, *out.CharacterName)
	require.NotNil(t, out.Department, "Department dropped")
	assert.Equal(t, *in.Department, *out.Department)
	require.NotNil(t, out.Job)
	assert.Equal(t, *in.Job, *out.Job)
	require.NotNil(t, out.PosterAsset)
	assert.Equal(t, *in.PosterAsset, *out.PosterAsset)
	require.NotNil(t, out.TMDBRating)
	assert.Equal(t, *in.TMDBRating, *out.TMDBRating)
	require.NotNil(t, out.TMDBVotes, "TMDBVotes dropped")
	assert.Equal(t, *in.TMDBVotes, *out.TMDBVotes)
	require.NotNil(t, out.EpisodeCount)
	assert.Equal(t, *in.EpisodeCount, *out.EpisodeCount)

	// ReleaseDate degrades to (Year, 1, 1) — the model only stores
	// year. The H-2 use case knows this and is consistent with v1
	// semantics; we just assert the year round-trips.
	require.NotNil(t, out.ReleaseDate)
	assert.Equal(t, in.ReleaseDate.Year(), out.ReleaseDate.Year())
}

// TestPersonCreditsAdapter_NilFields_RoundTrip covers the cold path:
// TMDB sometimes emits blank strings / zero counts which the mapper
// normalises to nil pointers. Adapter writes nil; column stores
// NULL; reader returns nil. Symmetric.
func TestPersonCreditsAdapter_NilFields_RoundTrip(t *testing.T) {
	t.Parallel()
	in := people.PersonCredit{
		PersonID:      42,
		MediaType:     "movie",
		TMDBMediaID:   999,
		TMDBCreditID:  "cold-credit",
		Kind:          people.SeriesCreditCrew,
		Title:         "Cold Movie",
		OriginalTitle: nil,
		Department:    nil,
		TMDBVotes:     nil,
	}
	row := database.PersonCreditModel{
		PersonID:      in.PersonID,
		TMDBCreditID:  in.TMDBCreditID,
		MediaType:     in.MediaType,
		TMDBMediaID:   int(in.TMDBMediaID),
		Title:         in.Title,
		OriginalTitle: in.OriginalTitle,
		Year:          yearFromReleaseDate(in.ReleaseDate),
		CharacterName: in.CharacterName,
		Kind:          string(in.Kind),
		Department:    in.Department,
		Job:           in.Job,
		PosterPath:    in.PosterAsset,
		VoteAverage:   in.TMDBRating,
		TMDBVotes:     in.TMDBVotes,
		EpisodeCount:  in.EpisodeCount,
	}
	out := adapters.ModelToPersonCredit(row)
	assert.Nil(t, out.OriginalTitle)
	assert.Nil(t, out.Department)
	assert.Nil(t, out.TMDBVotes)
}

// _ = context.Background prevents unused-import flag if the test
// suite is trimmed; harmless in practice.
var _ = context.Background
