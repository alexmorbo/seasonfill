package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichmentapp "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	enrichmentdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// recPayload mirrors tmdb.TVRecommendation just for fixture clarity.
// Story 571 B-54: BackdropPath field added so the A3b integration test
// can seed both media paths and assert both land via UpdateRecCanonMedia.
type recPayload struct {
	ID           int64
	Name         string
	Overview     string
	PosterPath   string
	BackdropPath string
}

type tmdbResponse struct {
	Recommendations []recPayload
}

// a3bFakeTMDB is the minimal TMDBClient impl the A3b integration test
// needs. Only GetTV is exercised; the others panic so any drift surfaces
// loud. Implements internal/enrichment/app.TMDBClient.
type a3bFakeTMDB struct {
	resp *tmdb.TVResponse
}

func newA3bFakeTMDB(payload *tmdbResponse) *a3bFakeTMDB {
	recs := make([]tmdb.TVRecommendation, 0, len(payload.Recommendations))
	for _, r := range payload.Recommendations {
		recs = append(recs, tmdb.TVRecommendation{
			ID:           r.ID,
			Name:         r.Name,
			Overview:     r.Overview,
			PosterPath:   r.PosterPath,
			BackdropPath: r.BackdropPath,
		})
	}
	return &a3bFakeTMDB{
		resp: &tmdb.TVResponse{
			Recommendations: &tmdb.TVRecommendations{Results: recs},
		},
	}
}

func (f *a3bFakeTMDB) GetTV(_ context.Context, _ int64, _ string) (*tmdb.TVResponse, error) {
	return f.resp, nil
}

func (f *a3bFakeTMDB) GetSeason(_ context.Context, _ int64, _ int, _ string) (*tmdb.SeasonResponse, error) {
	panic("a3bFakeTMDB.GetSeason should not be called in A3b integration test")
}

func (f *a3bFakeTMDB) GetPerson(_ context.Context, _ int64, _ string) (*tmdb.PersonResponse, error) {
	panic("a3bFakeTMDB.GetPerson should not be called in A3b integration test")
}

func (f *a3bFakeTMDB) FindByTVDB(_ context.Context, _ domain.TVDBID) (*tmdb.FindResponse, error) {
	panic("a3bFakeTMDB.FindByTVDB should not be called in A3b integration test")
}

// --- Minimal nop adapters satisfying every required SeriesWorkerDeps port.
// RefreshRecommendations only touches TMDB/Tx/Series/SeriesTexts/Recommendations
// but the constructor validates every port is non-nil. These nop fakes panic
// on any unexpected call so spurious dependency drift surfaces loud.

type nopSeasonsRepo struct{}

func (nopSeasonsRepo) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonSeason, error) {
	return nil, nil
}
func (nopSeasonsRepo) Upsert(_ context.Context, _ series.CanonSeason) (int64, error) {
	panic("nopSeasonsRepo.Upsert called")
}
func (nopSeasonsRepo) MarkSeasonEpisodesSynced(_ context.Context, _ domain.SeriesID, _ int, _ time.Time) error {
	panic("nopSeasonsRepo.MarkSeasonEpisodesSynced called")
}

type nopEpisodesRepo struct{}

func (nopEpisodesRepo) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonEpisode, error) {
	return nil, nil
}
func (nopEpisodesRepo) BatchUpsert(_ context.Context, _ []series.CanonEpisode) ([]int64, error) {
	panic("nopEpisodesRepo.BatchUpsert called")
}

type nopEpisodeTextsRepo struct{}

func (nopEpisodeTextsRepo) Upsert(_ context.Context, _ series.EpisodeText) error {
	panic("nopEpisodeTextsRepo.Upsert called")
}

type nopPeopleRepo struct{}

func (nopPeopleRepo) GetByTMDBID(_ context.Context, _ domain.TMDBID) (people.Person, error) {
	return people.Person{}, errors.New("nop")
}
func (nopPeopleRepo) Upsert(_ context.Context, _ people.Person) (int64, error) {
	panic("nopPeopleRepo.Upsert called")
}

type nopPersonCredits struct{}

func (nopPersonCredits) BatchUpsert(_ context.Context, _ []people.PersonCredit) ([]int64, error) {
	panic("nopPersonCredits.BatchUpsert called")
}

type nopGenres struct{}

func (nopGenres) Upsert(_ context.Context, _ taxonomy.Genre) (int64, error) {
	panic("nopGenres.Upsert called")
}
func (nopGenres) UpsertI18n(_ context.Context, _ int64, _ string, _ string) error {
	panic("nopGenres.UpsertI18n called")
}
func (nopGenres) Set(_ context.Context, _ domain.SeriesID, _ []int64) error {
	panic("nopGenres.Set called")
}

type nopKeywords struct{}

func (nopKeywords) Upsert(_ context.Context, _ taxonomy.Keyword) (int64, error) {
	panic("nopKeywords.Upsert called")
}
func (nopKeywords) UpsertI18n(_ context.Context, _ int64, _ string, _ string) error {
	panic("nopKeywords.UpsertI18n called")
}
func (nopKeywords) Set(_ context.Context, _ domain.SeriesID, _ []int64) error {
	panic("nopKeywords.Set called")
}

type nopNetworks struct{}

func (nopNetworks) Upsert(_ context.Context, _ taxonomy.Network) (int64, error) {
	panic("nopNetworks.Upsert called")
}
func (nopNetworks) Set(_ context.Context, _ domain.SeriesID, _ []int64) error {
	panic("nopNetworks.Set called")
}

type nopCompanies struct{}

func (nopCompanies) Upsert(_ context.Context, _ taxonomy.ProductionCompany) (int64, error) {
	panic("nopCompanies.Upsert called")
}
func (nopCompanies) Set(_ context.Context, _ domain.SeriesID, _ []int64) error {
	panic("nopCompanies.Set called")
}

type nopVideos struct{}

func (nopVideos) Upsert(_ context.Context, _ enrichmentapp.VideoRow) error {
	panic("nopVideos.Upsert called")
}

type nopContentRatings struct{}

func (nopContentRatings) Upsert(_ context.Context, _ domain.SeriesID, _ string, _ string) error {
	panic("nopContentRatings.Upsert called")
}

type nopExternalIDs struct{}

func (nopExternalIDs) Upsert(_ context.Context, _ enrichmentdomain.EntityType, _ int64, _ string, _ string) error {
	panic("nopExternalIDs.Upsert called")
}

type nopEnrichmentErrors struct{}

func (nopEnrichmentErrors) RecordFailure(_ context.Context, _ enrichmentdomain.EnrichmentError) error {
	return nil
}
func (nopEnrichmentErrors) ClearOnSuccess(_ context.Context, _ enrichmentdomain.EntityType, _ int64, _ enrichmentdomain.Source) error {
	return nil
}
func (nopEnrichmentErrors) GetForEntity(_ context.Context, _ enrichmentdomain.EntityType, _ int64) ([]enrichmentdomain.EnrichmentError, error) {
	return nil, nil
}
func (nopEnrichmentErrors) ListDueForRetry(_ context.Context, _ enrichmentdomain.Source, _ time.Time, _ int) ([]enrichmentdomain.EnrichmentError, error) {
	return nil, nil
}
func (nopEnrichmentErrors) GetByEntitySource(_ context.Context, _ enrichmentdomain.EntityType, _ int64, _ enrichmentdomain.Source) (enrichmentdomain.EnrichmentError, error) {
	return enrichmentdomain.EnrichmentError{}, errors.New("nop")
}
