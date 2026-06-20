package adapters

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/application/people"
	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
)

// PersonCreditsAdapter projects repositories.PersonCredit rows down
// to the H-1 composer-internal PersonCreditRef shape (Story 216). The
// projection is cheap (two field copies) and keeps the application
// layer free of the repository's wide PersonCredit struct.
type PersonCreditsAdapter struct {
	R *repositories.PersonCreditsRepository
}

// NewPersonCreditsAdapter wraps the supplied repository.
func NewPersonCreditsAdapter(r *repositories.PersonCreditsRepository) PersonCreditsAdapter {
	return PersonCreditsAdapter{R: r}
}

// ListByPerson implements seriesdetail.PersonCreditsPort.
func (a PersonCreditsAdapter) ListByPerson(ctx context.Context, personID int64) ([]seriesdetail.PersonCreditRef, error) {
	rows, err := a.R.ListByPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	out := make([]seriesdetail.PersonCreditRef, 0, len(rows))
	for _, pc := range rows {
		out = append(out, seriesdetail.PersonCreditRef{
			MediaType:   pc.MediaType,
			TMDBMediaID: pc.TMDBMediaID,
		})
	}
	return out, nil
}

// PersonCreditsReaderAdapter projects PersonCreditsRepository onto
// the H-2 PersonCreditsReader port. The repository's ListByPerson
// returns []PersonCreditModel; the adapter converts to
// []dompeople.PersonCredit row by row.
type PersonCreditsReaderAdapter struct {
	R *repositories.PersonCreditsRepository
}

// NewPersonCreditsReaderAdapter wraps the supplied repository.
func NewPersonCreditsReaderAdapter(r *repositories.PersonCreditsRepository) PersonCreditsReaderAdapter {
	return PersonCreditsReaderAdapter{R: r}
}

// Assert interface satisfaction at compile time.
var _ people.PersonCreditsReader = PersonCreditsReaderAdapter{}

// ListByPerson implements people.PersonCreditsReader.
func (a PersonCreditsReaderAdapter) ListByPerson(ctx context.Context, personID int64) ([]dompeople.PersonCredit, error) {
	rows, err := a.R.ListByPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	out := make([]dompeople.PersonCredit, 0, len(rows))
	for _, m := range rows {
		out = append(out, ModelToPersonCredit(m))
	}
	return out, nil
}

// ModelToPersonCredit maps PersonCreditModel → domain PersonCredit.
// Year passes through as the synthetic date (year, 1, 1) so
// downstream code that reads Year from ReleaseDate works;
// PosterPath is mapped to PosterAsset (the v1 H-2 layer treats both
// as pass-through strings, formal asset migration deferred).
//
// Exported so the round-trip test in cmd/server can drive the
// projection without spinning up gorm.
func ModelToPersonCredit(m database.PersonCreditModel) dompeople.PersonCredit {
	var rel *time.Time
	if m.Year != nil {
		t := time.Date(*m.Year, 1, 1, 0, 0, 0, 0, time.UTC)
		rel = &t
	}
	return dompeople.PersonCredit{
		ID:            m.ID,
		PersonID:      m.PersonID,
		MediaType:     m.MediaType,
		TMDBMediaID:   int64(m.TMDBMediaID),
		TMDBCreditID:  m.TMDBCreditID,
		Kind:          dompeople.SeriesCreditKind(m.Kind),
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		CharacterName: m.CharacterName,
		Department:    m.Department,
		Job:           m.Job,
		EpisodeCount:  m.EpisodeCount,
		ReleaseDate:   rel,
		PosterAsset:   m.PosterPath,
		TMDBRating:    m.VoteAverage,
		TMDBVotes:     m.TMDBVotes,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}
