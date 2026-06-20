package adapters

import (
	"context"

	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/internal/enrichment/app/people"
	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// PeopleReaderAdapter projects PeopleRepository onto the H-2
// PeopleReader port — GetByTMDBID for the hot resolution path,
// GetWithBio (renamed from repo's Get) for the bio-resolving
// path. The renaming is local; the production repository's
// method is `Get(ctx, id, language)`.
type PeopleReaderAdapter struct {
	R *repositories.PeopleRepository
}

// NewPeopleReaderAdapter wraps the supplied repository.
func NewPeopleReaderAdapter(r *repositories.PeopleRepository) PeopleReaderAdapter {
	return PeopleReaderAdapter{R: r}
}

// Assert interface satisfaction at compile time.
var _ people.PeopleReader = PeopleReaderAdapter{}

// GetByTMDBID implements people.PeopleReader.
func (a PeopleReaderAdapter) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (dompeople.Person, error) {
	return a.R.GetByTMDBID(ctx, tmdbID)
}

// GetWithBio implements people.PeopleReader.
func (a PeopleReaderAdapter) GetWithBio(ctx context.Context, id int64, language string) (dompeople.Person, error) {
	return a.R.Get(ctx, id, language)
}
