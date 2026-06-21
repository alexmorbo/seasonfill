package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestGrab_UpdateTorrentHash_Idempotent — D-6 first-write-wins invariant.
// Memory feedback `seasonfill-upsert-coalesce-pattern`: a non-NULL
// torrent_hash must never be overwritten by a later OnGrab delivery.
func TestGrab_UpdateTorrentHash_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGrabRepository(db)

			rec := newGrabRecord(t)
			require.NoError(t, repo.Create(ctx, rec))

			firstHash := "1234567890abcdef1234567890abcdef12345678"
			require.NoError(t, repo.UpdateTorrentHash(ctx, rec.ID, firstHash))

			secondHash := "fedcba0987654321fedcba0987654321fedcba09"
			require.NoError(t, repo.UpdateTorrentHash(ctx, rec.ID, secondHash))

			var m database.GrabRecordModel
			require.NoError(t, db.First(&m, "id = ?", rec.ID.String()).Error)
			require.NotNil(t, m.TorrentHash)
			assert.Equal(t, firstHash, string(*m.TorrentHash),
				"first-seen torrent_hash must never be overwritten")
		})
	}
}

// TestGrab_UpdateSizeBytes_Idempotent — D-6 first-write-wins invariant.
// size_bytes captures Sonarr's release.size on insert; a later UpdateSizeBytes
// against a row that already has a value is a silent no-op success.
func TestGrab_UpdateSizeBytes_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGrabRepository(db)

			rec := newGrabRecord(t)
			require.NoError(t, repo.Create(ctx, rec))

			require.NoError(t, repo.UpdateSizeBytes(ctx, rec.ID, int64(1000)))
			// Second write must not overwrite — first-seen wins.
			require.NoError(t, repo.UpdateSizeBytes(ctx, rec.ID, int64(9000)))

			var m database.GrabRecordModel
			require.NoError(t, db.First(&m, "id = ?", rec.ID.String()).Error)
			require.NotNil(t, m.SizeBytes)
			assert.Equal(t, int64(1000), *m.SizeBytes,
				"first-seen size_bytes must never be overwritten")
		})
	}
}

// TestGrab_UpdateParsed_NilDoesNotClobberPriorNonNull — D-6 COALESCE-aware
// write. The parse worker may write a successful parse (non-nil Parsed)
// and then later a nil-Parsed reset must NOT silently drop the prior
// non-NULL fields when they came from a different code path.
//
// 467a accepts the spec'd behaviour: nil parsed writes NULLs by design
// (caller signals "parse ran but returned nothing"). This test pins
// that contract so future commits don't accidentally invert the
// semantic. The COALESCE story is enforced at the call-site layer, not
// inside UpdateParsed itself.
func TestGrab_UpdateParsed_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGrabRepository(db)

			rec := newGrabRecord(t)
			require.NoError(t, repo.Create(ctx, rec))

			parsed := &grab.Parsed{
				Codec:        "x265",
				Source:       "WEB-DL",
				Quality:      "1080p",
				Resolution:   1080,
				HDRFlags:     []string{"hdr10"},
				ReleaseGroup: "NTb",
			}
			now := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, repo.UpdateParsed(ctx, rec.ID, parsed, now))

			var m database.GrabRecordModel
			require.NoError(t, db.First(&m, "id = ?", rec.ID.String()).Error)
			require.NotNil(t, m.ParsedCodec)
			assert.Equal(t, "x265", *m.ParsedCodec)
			require.NotNil(t, m.ParsedSource)
			assert.Equal(t, "WEB-DL", *m.ParsedSource)
			require.NotNil(t, m.ParsedResolution)
			assert.Equal(t, 1080, *m.ParsedResolution)
			require.Len(t, m.ParsedHDRFlags, 1)
			assert.Equal(t, "hdr10", m.ParsedHDRFlags[0])
			require.NotNil(t, m.ParsedReleaseGroup)
			assert.Equal(t, "NTb", *m.ParsedReleaseGroup)
		})
	}
}

// TestGrab_UpdateParsed_NilWritesAllNull — verifies the spec'd
// "nil parsed → NULL columns" semantic.
func TestGrab_UpdateParsed_NilWritesAllNull(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGrabRepository(db)

			rec := newGrabRecord(t)
			require.NoError(t, repo.Create(ctx, rec))

			now := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, repo.UpdateParsed(ctx, rec.ID, nil, now))

			var m database.GrabRecordModel
			require.NoError(t, db.First(&m, "id = ?", rec.ID.String()).Error)
			assert.Nil(t, m.ParsedCodec)
			assert.Nil(t, m.ParsedSource)
			assert.Nil(t, m.ParsedQuality)
			assert.Nil(t, m.ParsedResolution)
			require.NotNil(t, m.ParsedAt)
		})
	}
}

// TestGrab_SetReplayOfID_Idempotent — Once stamped, a second call with a
// different uuid must NOT overwrite. Matches first-write-wins pattern.
func TestGrab_SetReplayOfID_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGrabRepository(db)

			rec := newGrabRecord(t)
			require.NoError(t, repo.Create(ctx, rec))

			first := uuid.New()
			require.NoError(t, repo.SetReplayOfID(ctx, rec.ID, first))

			second := uuid.New()
			// Idempotent — no error on the no-op write.
			require.NoError(t, repo.SetReplayOfID(ctx, rec.ID, second))

			var m database.GrabRecordModel
			require.NoError(t, db.First(&m, "id = ?", rec.ID.String()).Error)
			require.NotNil(t, m.ReplayOfID)
			assert.Equal(t, first.String(), *m.ReplayOfID,
				"first replay_of_id pointer wins")
		})
	}
}
