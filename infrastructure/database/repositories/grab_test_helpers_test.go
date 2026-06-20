package repositories

// Test helpers that span multiple repositories' tests. They moved here
// from grab_repository_test.go + grab_list_test.go during story 431
// (A-1-5) when the grab repository graduated to internal/grab/persistence.
//
// The original definitions still exist in their new home; this file
// keeps the shorter signatures available to neighbouring repo tests
// (companies, episode_people, genres, keywords, recommendations,
// counter) without forcing them to import internal/grab/persistence
// (which would create a backward layer dependency and trip the
// vertical-slice depcheck guards).
//
// Future story (D-0+ or 449 model split) will relocate these into
// internal/shared/testhelpers; until then they live alongside the catalog
// repositories that depend on them.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// seedGrab inserts a minimal grab record via the production
// internal/grab/persistence.GrabRepository.Create path so the catalog
// tests still exercise the real write codepath (no shortcut through
// raw gorm).
func seedGrab(t *testing.T, db *gorm.DB, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, status grab.Status, createdAt time.Time) grab.Record {
	t.Helper()
	rec := grab.Record{
		ID:           uuid.New(),
		InstanceName: instance,
		SeriesID:     seriesID,
		SeriesTitle:  "Hijack",
		SeasonNumber: season,
		ReleaseGUID:  uuid.NewString(),
		ReleaseTitle: "S02 Pack",
		IndexerID:    3,
		IndexerName:  "RT",
		Status:       status,
		ScanRunID:    uuid.New(),
		Attempts:     1,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	require.NoError(t, grabpersistence.NewGrabRepository(db).Create(context.Background(), rec))
	return rec
}

// ptrTMDBID / ptrIMDBID / ptrTVDBID are thin pointer-to-typed-ID
// shorthands used by the taxonomy + companies + episode_people tests.
func ptrTMDBID(i int) *domain.TMDBID {
	v := domain.TMDBID(i)
	return &v
}

func ptrIMDBID(s string) *domain.IMDBID {
	v := domain.IMDBID(s)
	return &v
}

func ptrTVDBID(i int) *domain.TVDBID {
	v := domain.TVDBID(i)
	return &v
}
