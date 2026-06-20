package persistence

// Test helpers that span multiple repositories' tests. Story 437
// (A-1-11) carried these here from infrastructure/database/
// repositories/grab_test_helpers_test.go when the catalog repos
// graduated to internal/enrichment/persistence. The original file
// still hosts the same definitions for the stays (counter +
// remaining sync/audit repos) — see the doc comment there.
//
// Future story (D-0+ or 449 model split) will relocate the ptr*
// helpers into internal/shared/testhelpers and the seedGrab helper
// into internal/grab/persistence's test helpers; until then they
// live alongside the catalog repositories that depend on them.

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
// raw gorm). Currently used by the in-flight commit-2 (people-data)
// and commit-3 (taxonomy/i18n) moves; the catalog-data sub-group in
// commit 1 does not exercise it yet — the //nolint:unused pragma is
// dropped as soon as the people / taxonomy tests land here.
//
//nolint:unused // see commit-2 / commit-3 of story 437.
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
// shorthands used by the catalog series / seasons / episodes tests
// (commit 1) and the taxonomy + companies + episode_people tests
// (commits 2 + 3).
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
