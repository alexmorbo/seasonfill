package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

func newGrabRecord(t *testing.T) grab.Record {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	return grab.Record{
		ID:           uuid.New(),
		InstanceName: "main",
		SeriesID:     122,
		SeriesTitle:  "Hijack",
		SeasonNumber: 2,
		ReleaseGUID:  "g1",
		ReleaseTitle: "Hijack.S02.PACK",
		DownloadID:   "ABC123",
		IndexerID:    3,
		IndexerName:  "RT",
		Status:       grab.StatusGrabbed,
		ScanRunID:    uuid.New(),
		Attempts:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func TestGrabRepository_Create_Success(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(context.Background(), rec))

	var m database.GrabRecordModel
	require.NoError(t, db.First(&m, "id = ?", rec.ID.String()).Error)
	assert.Equal(t, "grabbed", m.Status)
	assert.Equal(t, "ABC123", m.DownloadID)
}

func TestGrabRepository_Create_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = repo.Create(context.Background(), grab.Record{
		ID: uuid.New(), Status: grab.StatusGrabbed,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		ScanRunID: uuid.New(),
	})
	require.Error(t, err)
}

func TestGrabRepository_MatchLatest_ByDownloadID_Found(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.MatchLatest(ctx, ports.MatchKey{
		DownloadID: rec.DownloadID, InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
}

func TestGrabRepository_MatchLatest_TerminalRowExcluded(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	rec.Status = grab.StatusImported
	require.NoError(t, repo.Create(ctx, rec))

	_, err := repo.MatchLatest(ctx, ports.MatchKey{
		DownloadID: rec.DownloadID, InstanceName: rec.InstanceName,
	})
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGrabRepository_MatchLatest_FallbackByTuple_Found(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	rec.DownloadID = ""
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.MatchLatest(ctx, ports.MatchKey{
		ReleaseTitle: rec.ReleaseTitle,
		SeriesID:     rec.SeriesID,
		SeasonNumber: rec.SeasonNumber,
		InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
}

func TestGrabRepository_MatchLatest_DownloadIDMisses_FallsThroughToTuple(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	rec.DownloadID = ""
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.MatchLatest(ctx, ports.MatchKey{
		DownloadID:   "UNKNOWN",
		ReleaseTitle: rec.ReleaseTitle,
		SeriesID:     rec.SeriesID,
		SeasonNumber: rec.SeasonNumber,
		InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
}

func TestGrabRepository_MatchLatest_NoMatch_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	_, err := repo.MatchLatest(context.Background(), ports.MatchKey{
		DownloadID: "missing", InstanceName: "main",
	})
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGrabRepository_UpdateStatus_Success_WithMessage(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(ctx, rec))

	require.NoError(t, repo.UpdateStatus(ctx, rec.ID, grab.StatusImportFailed, "bad file"))

	var got database.GrabRecordModel
	require.NoError(t, db.First(&got, "id = ?", rec.ID.String()).Error)
	assert.Equal(t, "import_failed", got.Status)
	assert.Equal(t, "bad file", got.ErrorMessage)
}

func TestGrabRepository_UpdateStatus_UnknownID_ErrNotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	err := repo.UpdateStatus(context.Background(), uuid.New(), grab.StatusImported, "")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGrabRepository_UpdateStatus_TerminalSource_Rejects(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	rec.Status = grab.StatusImported
	require.NoError(t, repo.Create(ctx, rec))

	err := repo.UpdateStatus(ctx, rec.ID, grab.StatusImportFailed, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, grab.ErrInvalidStatusTransition))
}

func TestGrabRepository_Create_SameFourTupleTwice(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()

	first := newGrabRecord(t)
	require.NoError(t, repo.Create(ctx, first))

	second := newGrabRecord(t)
	second.ID = uuid.New()
	second.CreatedAt = first.CreatedAt.Add(time.Second)
	second.UpdatedAt = second.CreatedAt
	require.NoError(t, repo.Create(ctx, second),
		"second grab on identical 4-tuple must succeed")

	instance := first.InstanceName
	got, _, err := repo.List(ctx,
		ports.GrabFilter{Instance: &instance},
		ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 2, "both rows must be visible")
	assert.Equal(t, second.ID, got[0].ID, "newest first")
	assert.Equal(t, first.ID, got[1].ID)
}

func TestGrabRepository_UpdateTorrentHash_Success_FromNull(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(context.Background(), rec))

	const hash = "0123456789abcdef0123456789abcdef01234567"
	require.NoError(t, repo.UpdateTorrentHash(context.Background(), rec.ID, hash))

	got, err := repo.MatchLatest(context.Background(), ports.MatchKey{
		DownloadID:   rec.DownloadID,
		InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	require.NotNil(t, got.TorrentHash)
	assert.Equal(t, hash, *got.TorrentHash)
}

func TestGrabRepository_UpdateTorrentHash_Idempotent_DoesNotOverwrite(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	rec := newGrabRecord(t)
	const original = "0123456789abcdef0123456789abcdef01234567"
	rec.TorrentHash = &([]string{original}[0])
	require.NoError(t, repo.Create(context.Background(), rec))

	const newer = "fedcba9876543210fedcba9876543210fedcba98"
	require.NoError(t, repo.UpdateTorrentHash(context.Background(), rec.ID, newer))

	got, err := repo.MatchLatest(context.Background(), ports.MatchKey{
		DownloadID:   rec.DownloadID,
		InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	require.NotNil(t, got.TorrentHash)
	assert.Equal(t, original, *got.TorrentHash,
		"UpdateTorrentHash must NOT overwrite an already-set hash (D63 first-seen-wins)")
}

func TestGrabRepository_UpdateTorrentHash_UnknownID_ErrNotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	err := repo.UpdateTorrentHash(context.Background(), uuid.New(),
		"0123456789abcdef0123456789abcdef01234567")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGrabRepository_UpdateTorrentHash_EmptyHash_NoOp(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(context.Background(), rec))

	require.NoError(t, repo.UpdateTorrentHash(context.Background(), rec.ID, ""))

	got, err := repo.MatchLatest(context.Background(), ports.MatchKey{
		DownloadID:   rec.DownloadID,
		InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	assert.Nil(t, got.TorrentHash, "empty hash must be a no-op — column stays NULL")
}

func TestGrabRepository_FindLatestSuccessByHash_Match(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()

	hash := "abcdef0123456789abcdef0123456789abcdef01"
	older := buildSuccessRec(t, "alpha", 122, 2, "guid-1", hash)
	older.CreatedAt = time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	newer := buildSuccessRec(t, "alpha", 122, 2, "guid-2", hash)
	newer.CreatedAt = time.Date(2026, 6, 6, 11, 0, 0, 0, time.UTC)
	require.NoError(t, repo.Create(ctx, older))
	require.NoError(t, repo.Create(ctx, newer))

	got, err := repo.FindLatestSuccessByHash(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, newer.ID, got.ID, "newest row wins")
}

func TestGrabRepository_FindLatestSuccessByHash_ExcludesGrabFailed(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()

	hash := "abcdef0123456789abcdef0123456789abcdef01"
	failed := buildSuccessRec(t, "alpha", 122, 2, "guid-failed", hash)
	failed.Status = grab.StatusGrabFailed
	require.NoError(t, repo.Create(ctx, failed))

	_, err := repo.FindLatestSuccessByHash(ctx, hash)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound),
		"grab_failed rows are not matchable — they don't represent an on-disk torrent")
}

func TestGrabRepository_FindLatestSuccessByHash_EmptyHash(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	_, err := repo.FindLatestSuccessByHash(context.Background(), "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestGrabRepository_FindLatestSuccessByHash_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	_, err := repo.FindLatestSuccessByHash(context.Background(),
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestGrabRepository_CreateReplay_PopulatesReplayOfID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()

	original := buildSuccessRec(t, "alpha", 122, 2, "guid-orig", "")
	require.NoError(t, repo.Create(ctx, original))

	replay := buildSuccessRec(t, "alpha", 122, 2, "guid-replay", "")
	require.NoError(t, repo.CreateReplay(ctx, replay, original.ID))

	// Round-trip via List — confirms ReplayOfID lands in the DB and
	// comes back unmarshalled.
	rows, _, err := repo.List(ctx, ports.GrabFilter{
		Instance:     ptrString("alpha"),
		SeriesID:     ptrInt(122),
		SeasonNumber: ptrInt(2),
	}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 2)

	// rows[0] is the newest by created_at DESC.
	var seenReplay bool
	for _, r := range rows {
		if r.ID == replay.ID {
			require.NotNil(t, r.ReplayOfID)
			assert.Equal(t, original.ID, *r.ReplayOfID)
			seenReplay = true
		}
		if r.ID == original.ID {
			assert.Nil(t, r.ReplayOfID, "original carries no ReplayOfID")
		}
	}
	assert.True(t, seenReplay, "replay row was found")
}

// buildSuccessRec is a local helper that builds a grab.Record fixture
// with a fresh uuid + status=grabbed + the supplied (instance, series,
// season, guid, hash). All other fields are populated with sensible
// defaults the DB INSERT accepts.
func buildSuccessRec(t *testing.T, instance string, seriesID, season int, guid, hash string) grab.Record {
	t.Helper()
	rec := grab.Record{
		ID:                uuid.New(),
		InstanceName:      instance,
		SeriesID:          seriesID,
		SeriesTitle:       "Test Series",
		SeasonNumber:      season,
		ReleaseGUID:       guid,
		ReleaseTitle:      guid + " title",
		IndexerID:         1,
		IndexerName:       "indexer-x",
		CustomFormatScore: 100,
		Quality:           "WEB-DL 1080p",
		CoverageCount:     10,
		Status:            grab.StatusGrabbed,
		ScanRunID:         uuid.New(),
		Attempts:          1,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	if hash != "" {
		h := hash
		rec.TorrentHash = &h
	}
	return rec
}

func ptrString(s string) *string { return &s }
func ptrInt(i int) *int          { return &i }

func TestGrabRepository_ListReplaysOf_EmptyParents_EmptyResult(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	out, err := repo.ListReplaysOf(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestGrabRepository_ListReplaysOf_NoChildren_AbsentFromMap(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(ctx, rec))

	out, err := repo.ListReplaysOf(ctx, []uuid.UUID{rec.ID})
	require.NoError(t, err)
	_, has := out[rec.ID]
	assert.False(t, has, "leaf with no children must be absent from map")
}

func TestGrabRepository_ListReplaysOf_ChainOfThree(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()

	parent := newGrabRecord(t)
	require.NoError(t, repo.Create(ctx, parent))

	mid := newGrabRecord(t)
	mid.ID = uuid.New()
	mid.ReleaseGUID = "g2"
	mid.ReleaseTitle = "Hijack.S02.PACK.v2"
	mid.ReplayOfID = &parent.ID
	mid.CreatedAt = parent.CreatedAt.Add(time.Hour)
	require.NoError(t, repo.Create(ctx, mid))

	leaf := newGrabRecord(t)
	leaf.ID = uuid.New()
	leaf.ReleaseGUID = "g3"
	leaf.ReleaseTitle = "Hijack.S02.PACK.v3"
	leaf.ReplayOfID = &mid.ID
	leaf.CreatedAt = parent.CreatedAt.Add(2 * time.Hour)
	require.NoError(t, repo.Create(ctx, leaf))

	out, err := repo.ListReplaysOf(ctx, []uuid.UUID{parent.ID, mid.ID, leaf.ID})
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, []uuid.UUID{mid.ID}, out[parent.ID])
	assert.Equal(t, []uuid.UUID{leaf.ID}, out[mid.ID])
	_, hasLeaf := out[leaf.ID]
	assert.False(t, hasLeaf, "leaf with no children must be absent")
}

func TestGrabRepository_ListReplaysOf_RespectsCap(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()

	parent := newGrabRecord(t)
	require.NoError(t, repo.Create(ctx, parent))

	total := ports.MaxReplaysPerParent + 10
	for i := 0; i < total; i++ {
		c := newGrabRecord(t)
		c.ID = uuid.New()
		c.ReleaseGUID = "g_" + uuid.New().String()
		c.ReplayOfID = &parent.ID
		c.CreatedAt = parent.CreatedAt.Add(time.Duration(i) * time.Minute)
		require.NoError(t, repo.Create(ctx, c))
	}

	out, err := repo.ListReplaysOf(ctx, []uuid.UUID{parent.ID})
	require.NoError(t, err)
	assert.Len(t, out[parent.ID], ports.MaxReplaysPerParent,
		"server-side cap must be enforced")
}
