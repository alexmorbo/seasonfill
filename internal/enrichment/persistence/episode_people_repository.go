package persistence

import (
	"context"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// EpisodePeopleRepository wrapped the legacy `episode_people` table.
// The table was dropped in D-1 — episode-level credits now live in
// `person_credits` with media_type='tv_episode',
// tmdb_media_id=<episode tmdb_id>.
//
// D-3 (story 464c) drops the backing schema; consumer rewrite
// (seriesdetail composer guest-cast branch) is owned by D-7. Every
// method panics with the canonical "pending D-7" sentinel.
type EpisodePeopleRepository struct {
	db *gorm.DB
}

func NewEpisodePeopleRepository(db *gorm.DB) *EpisodePeopleRepository {
	return &EpisodePeopleRepository{db: db}
}

func (r *EpisodePeopleRepository) Get(ctx context.Context, id int64) (people.EpisodeCredit, error) {
	_, _ = ctx, id
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); episode_people dropped in D-1, replaced by person_credits(media_type='tv_episode')")
}

func (r *EpisodePeopleRepository) ListByEpisode(ctx context.Context, episodeID domain.EpisodeID, kind people.EpisodeCreditKind) ([]people.EpisodeCredit, error) {
	_, _, _ = ctx, episodeID, kind
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); episode_people dropped in D-1, replaced by person_credits(media_type='tv_episode')")
}

func (r *EpisodePeopleRepository) Upsert(ctx context.Context, c people.EpisodeCredit) (int64, error) {
	_, _ = ctx, c
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); episode_people dropped in D-1, replaced by person_credits(media_type='tv_episode')")
}

func (r *EpisodePeopleRepository) BatchUpsert(ctx context.Context, credits []people.EpisodeCredit) ([]int64, error) {
	_, _ = ctx, credits
	panic("not implemented — pending D-7 i18n+seriesdetail rewrite (D2-revised-roadmap.md); episode_people dropped in D-1, replaced by person_credits(media_type='tv_episode')")
}
