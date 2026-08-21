package webhook

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// stubStateAdapter routes scan.MovieStateUpserter.Upsert → the THIN
// MovieStatesRepository.UpsertStub (the webhook writer).
type stubStateAdapter struct {
	repo *catalogpersistence.MovieStatesRepository
}

func (a stubStateAdapter) Upsert(ctx context.Context, e movie.StateEntry) error {
	return a.repo.UpsertStub(ctx, e)
}

// TestMovieUseCase_MovieAdded_WritesBothTables — synthetic Radarr webhook:
// MovieAdded lands BOTH the movies canon stub AND the movie_states row (through
// the shared F-21 helper); MovieDelete soft-deletes the state row.
func TestMovieUseCase_MovieAdded_WritesBothTables(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := enrichpersistence.NewMovieRepository(db)
			states := catalogpersistence.NewMovieStatesRepository(db)
			uc := NewMovieUseCase(MovieDeps{
				Movies:      movies,
				States:      stubStateAdapter{states}, // THIN UpsertStub
				SoftDeleter: states,
			})

			evt := domainwebhook.MovieEvent{
				Type: domainwebhook.MovieEventTypeUpsert, InstanceName: "radarr-main",
				RadarrMovieID: 7, Title: "Dune", TitleSlug: "dune-2021", Year: 2021,
				TMDBID: 438631, IMDBID: "tt1160419", Monitored: true, HasFile: false,
				MinimumAvailability: "released", RawEventType: "MovieAdded",
			}
			require.NoError(t, uc.Process(ctx, evt))

			// movie_states row landed and is active.
			st, err := states.Get(ctx, "radarr-main", 7)
			require.NoError(t, err)
			require.NotZero(t, st.MovieID)
			assert.True(t, st.IsActive())
			assert.True(t, st.AddedToRadarr)
			require.NotNil(t, st.Availability)
			assert.Equal(t, "released", *st.Availability)

			// movies canon row landed (COALESCE-safe stub) with the tmdb_id.
			mv, err := movies.Get(ctx, st.MovieID)
			require.NoError(t, err)
			require.NotNil(t, mv.TMDBID)
			assert.Equal(t, domain.TMDBID(438631), *mv.TMDBID)

			// MovieDelete → soft-delete.
			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type: domainwebhook.MovieEventTypeDeleted, InstanceName: "radarr-main", RadarrMovieID: 7,
			}))
			st, err = states.Get(ctx, "radarr-main", 7)
			require.NoError(t, err)
			assert.False(t, st.IsActive(), "soft-deleted by webhook")
		})
	}
}

// TestMovieUseCase_TestPing_NoWrite — an Unsupported (Test/Health) event is a
// no-op: nothing is written to movie_states.
func TestMovieUseCase_TestPing_NoWrite(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			states := catalogpersistence.NewMovieStatesRepository(db)
			uc := NewMovieUseCase(MovieDeps{
				Movies:      enrichpersistence.NewMovieRepository(db),
				States:      stubStateAdapter{states},
				SoftDeleter: states,
			})

			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type: domainwebhook.MovieEventTypeUnsupported, InstanceName: "radarr-main",
				RadarrMovieID: 7, RawEventType: "Test",
			}))

			_, err := states.Get(ctx, "radarr-main", 7)
			require.ErrorIs(t, err, ports.ErrNotFound, "Test ping must not write a state row")
		})
	}
}

// TestMovieUseCase_WebhookDoesNotBlankEnrichedCanon — the prod COALESCE assert
// (mirror movie_repository_test.go:39): a TMDB-enriched movies row must survive
// a webhook stub write carrying nil enrichment columns.
func TestMovieUseCase_WebhookDoesNotBlankEnrichedCanon(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := enrichpersistence.NewMovieRepository(db)
			states := catalogpersistence.NewMovieStatesRepository(db)

			// Seed an enriched (full) canon.
			tmdb := domain.TMDBID(438631)
			rating := 8.1
			poster := "/p.jpg"
			status := "Released"
			enrichedID, err := movies.Upsert(ctx, movie.Canon{
				TMDBID:      &tmdb,
				Hydration:   movie.HydrationFull,
				Title:       "Dune",
				Status:      &status,
				PosterAsset: &poster,
				TMDBRating:  &rating,
			})
			require.NoError(t, err)
			require.NotZero(t, enrichedID)

			uc := NewMovieUseCase(MovieDeps{
				Movies:      movies,
				States:      stubStateAdapter{states},
				SoftDeleter: states,
			})
			// Webhook MovieAdded with the SAME tmdb_id, nil enrichment columns.
			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type: domainwebhook.MovieEventTypeUpsert, InstanceName: "radarr-main",
				RadarrMovieID: 7, Title: "Dune", TMDBID: 438631, Monitored: true,
				RawEventType: "MovieAdded",
			}))

			got, err := movies.Get(ctx, enrichedID)
			require.NoError(t, err)
			require.NotNil(t, got.TMDBRating)
			assert.InDelta(t, 8.1, *got.TMDBRating, 1e-9)
			require.NotNil(t, got.PosterAsset)
			assert.Equal(t, "/p.jpg", *got.PosterAsset)
			assert.Equal(t, movie.HydrationFull, got.Hydration, "hydration must stay full")
		})
	}
}

// TestMovieUseCase_TwoWriter_IdenticalStateRow — the F-21 persist-level proof:
// the SAME ports.RadarrMovie funnelled through the rich (sync) writer and the
// thin (webhook) writer produces byte-identical movie_states rows on a fresh
// INSERT (the two writers only diverge on a conflict UPDATE, where the thin
// writer preserves stats). Complements the pure-builder anti-drift test in
// internal/catalog/app/scan.
func TestMovieUseCase_TwoWriter_IdenticalStateRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			movies := enrichpersistence.NewMovieRepository(db)
			states := catalogpersistence.NewMovieStatesRepository(db)

			m := ports.RadarrMovie{
				RadarrMovieID: 7, Title: "Dune", TitleSlug: "dune-2021", Year: 2021,
				TMDBID: 438631, IMDBID: "tt1160419", Monitored: true, HasFile: true,
				MinimumAvailability: "released", SizeOnDiskBytes: 5_000_000_000,
			}
			now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)

			// Sync path (RICH repo writer) → instance "radarr-sync".
			syncCache := scan.BuildRadarrMovieCache("radarr-sync", m, now)
			_, err := scan.PersistRadarrMovieCache(ctx, movies, states, syncCache)
			require.NoError(t, err)

			// Webhook path (THIN stub adapter) → instance "radarr-webhook".
			webhookCache := scan.BuildRadarrMovieCache("radarr-webhook", m, now)
			_, err = scan.PersistRadarrMovieCache(ctx, movies, stubStateAdapter{states}, webhookCache)
			require.NoError(t, err)

			syncRow, err := states.Get(ctx, "radarr-sync", 7)
			require.NoError(t, err)
			webhookRow, err := states.Get(ctx, "radarr-webhook", 7)
			require.NoError(t, err)

			// Normalise the fields that legitimately differ (instance + the
			// repo-stamped UpdatedAt) before comparing.
			syncRow.InstanceName = ""
			webhookRow.InstanceName = ""
			syncRow.UpdatedAt = time.Time{}
			webhookRow.UpdatedAt = time.Time{}
			assert.Equal(t, syncRow, webhookRow, "rich and thin writers must land identical rows on fresh insert")
			// Both resolve to the SAME canon movie id (one real movie).
			assert.Equal(t, syncRow.MovieID, webhookRow.MovieID)
			require.NotNil(t, syncRow.Availability)
			assert.Equal(t, "released", *syncRow.Availability)
			assert.Equal(t, int64(5_000_000_000), syncRow.SizeOnDiskBytes)
		})
	}
}

// ─── ADR-0023 B1.2: Radarr Grab → torrent_movie_map ──────────────────────────

// fakeTorrentMovieMap records every Upsert/UpsertTx call. Movie twin of
// fakeTorrentSeriesMap (usecase_test.go) — the row type differs, so it cannot
// be reused.
type fakeTorrentMovieMap struct {
	mu   sync.Mutex
	rows []torrentsync.MovieMapRow
	err  error
}

func (f *fakeTorrentMovieMap) Upsert(_ context.Context, row torrentsync.MovieMapRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeTorrentMovieMap) UpsertTx(ctx context.Context, row torrentsync.MovieMapRow) error {
	return f.Upsert(ctx, row)
}

func (f *fakeTorrentMovieMap) snapshot() []torrentsync.MovieMapRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]torrentsync.MovieMapRow, len(f.rows))
	copy(out, f.rows)
	return out
}

// seedArrInstanceRow inserts the arr_instance parent row that
// torrent_movie_map.instance_name FK→arr_instance.name (migration 000065)
// requires. movie_states has no such FK, which is why the older tests in this
// file get away without it; SQLite without PRAGMA foreign_keys=on would too,
// but the D-0 bar demands the Postgres lane pass as well (same rationale as
// internal/catalog/persistence/qbit_test_helpers_test.go).
func seedArrInstanceRow(tb testing.TB, db *gorm.DB, name domain.InstanceName) {
	tb.Helper()
	const insertSQL = `INSERT INTO arr_instance (name, url, mode, health, transitions_count, type)
	                   VALUES (?, ?, ?, ?, ?, ?)
	                   ON CONFLICT (name) DO NOTHING`
	require.NoError(tb,
		db.Exec(insertSQL, string(name), "http://localhost", "auto", "unknown", 0, "radarr").Error,
	)
}

// TestMovieUseCase_Grabbed_WritesMovieMapRow — B1.2 end-to-end against the REAL
// repo + REAL GormTransactor: a Radarr Grab carrying a downloadId lands one
// torrent_movie_map row with source=webhook / provenance=radarr_search, and the
// hash is stored lowercased regardless of the payload's casing.
func TestMovieUseCase_Grabbed_WritesMovieMapRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seedArrInstanceRow(t, db, "radarr-main")

			states := catalogpersistence.NewMovieStatesRepository(db)
			mapRepo := catalogpersistence.NewTorrentMovieMapRepository(db)
			now := time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)
			uc := NewMovieUseCase(MovieDeps{
				Movies:          enrichpersistence.NewMovieRepository(db),
				States:          stubStateAdapter{states},
				SoftDeleter:     states,
				TorrentMovieMap: mapRepo,
				Tx:              catalogpersistence.NewGormTransactor(db),
				Logger:          quietLogger(),
			}).WithClock(func() time.Time { return now })

			const upperHash = "0123456789ABCDEF0123456789ABCDEF01234567"
			const lowerHash = "0123456789abcdef0123456789abcdef01234567"
			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type:          domainwebhook.MovieEventTypeGrabbed,
				InstanceName:  "radarr-main",
				RadarrMovieID: 42,
				Title:         "Dune",
				TMDBID:        438631,
				DownloadID:    upperHash,
				RawEventType:  "Grab",
			}))

			hashes, err := mapRepo.HashesForMovie(ctx, "radarr-main", domain.RadarrMovieID(42))
			require.NoError(t, err)
			require.Len(t, hashes, 1)
			assert.Equal(t, lowerHash, hashes[0], "hash must be normalised lowercase")

			var m database.TorrentMovieMapModel
			require.NoError(t, db.First(&m, "instance_name = ? AND torrent_hash = ?", "radarr-main", lowerHash).Error)
			assert.Equal(t, domain.RadarrMovieID(42), m.RadarrMovieID)
			assert.Equal(t, string(torrentsync.MovieMapSourceWebhook), m.Source)
			assert.Equal(t, string(torrentsync.MovieProvenanceRadarrSearch), m.Provenance)
			assert.True(t, m.CreatedAt.Equal(now))

			// Re-delivery is idempotent — first-source-wins, still one row.
			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type:          domainwebhook.MovieEventTypeGrabbed,
				InstanceName:  "radarr-main",
				RadarrMovieID: 42,
				DownloadID:    lowerHash,
				RawEventType:  "Grab",
			}))
			hashes, err = mapRepo.HashesForMovie(ctx, "radarr-main", domain.RadarrMovieID(42))
			require.NoError(t, err)
			assert.Len(t, hashes, 1, "webhook re-delivery must not duplicate the bridge row")

			// A Grab must NOT touch the movie cache (see handleMovieGrabbed
			// docstring: a grab's hasFile is always false).
			_, err = states.Get(ctx, "radarr-main", 42)
			require.ErrorIs(t, err, ports.ErrNotFound, "grab must not drive the movie_states cache write")
		})
	}
}

// TestMovieUseCase_Grabbed_MapWriteRunsInTx — the bridge write goes through the
// injected Transactor exactly once when one is wired.
func TestMovieUseCase_Grabbed_MapWriteRunsInTx(t *testing.T) {
	t.Parallel()
	mapRepo := &fakeTorrentMovieMap{}
	tx := &fakeTransactor{}
	now := time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)
	uc := NewMovieUseCase(MovieDeps{
		TorrentMovieMap: mapRepo,
		Tx:              tx,
		Logger:          quietLogger(),
	}).WithClock(func() time.Time { return now })

	const hash = "0123456789abcdef0123456789abcdef01234567"
	require.NoError(t, uc.Process(context.Background(), domainwebhook.MovieEvent{
		Type:          domainwebhook.MovieEventTypeGrabbed,
		InstanceName:  "radarr-main",
		RadarrMovieID: 42,
		DownloadID:    hash,
		RawEventType:  "Grab",
	}))

	assert.Equal(t, 1, tx.called, "bridge write must open exactly one tx")
	assert.True(t, tx.committed)
	rows := mapRepo.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, torrentsync.MovieMapRow{
		Instance:      "radarr-main",
		Hash:          hash,
		RadarrMovieID: domain.RadarrMovieID(42),
		Source:        torrentsync.MovieMapSourceWebhook,
		Provenance:    torrentsync.MovieProvenanceRadarrSearch,
		CreatedAt:     now,
	}, rows[0])
}

// TestMovieUseCase_Grabbed_MalformedDownloadID_NoWrite — fail-soft: an empty or
// unparseable downloadId writes nothing, opens no tx and still returns nil so
// the handler answers 200 (a mapping miss must never make Radarr retry).
func TestMovieUseCase_Grabbed_MalformedDownloadID_NoWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		downloadID string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"too_short", "0123456789abcdef"},
		{"non_hex", "zzzz456789abcdef0123456789abcdef01234567"},
		{"usenet_style_id", "SABnzbd_nzo_abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mapRepo := &fakeTorrentMovieMap{}
			tx := &fakeTransactor{}
			uc := NewMovieUseCase(MovieDeps{
				TorrentMovieMap: mapRepo,
				Tx:              tx,
				Logger:          quietLogger(),
			})

			require.NoError(t, uc.Process(context.Background(), domainwebhook.MovieEvent{
				Type:          domainwebhook.MovieEventTypeGrabbed,
				InstanceName:  "radarr-main",
				RadarrMovieID: 42,
				DownloadID:    tc.downloadID,
				RawEventType:  "Grab",
			}))
			assert.Empty(t, mapRepo.snapshot(), "no hash → no bridge row")
			assert.Zero(t, tx.called, "no work → no tx")
		})
	}
}

// TestMovieUseCase_Grabbed_MissingMovieID_NoWrite — a Grab without a movie
// block cannot be mapped; guard fires before the repo's own validation.
func TestMovieUseCase_Grabbed_MissingMovieID_NoWrite(t *testing.T) {
	t.Parallel()
	mapRepo := &fakeTorrentMovieMap{}
	uc := NewMovieUseCase(MovieDeps{TorrentMovieMap: mapRepo, Logger: quietLogger()})

	require.NoError(t, uc.Process(context.Background(), domainwebhook.MovieEvent{
		Type:         domainwebhook.MovieEventTypeGrabbed,
		InstanceName: "radarr-main",
		DownloadID:   "0123456789abcdef0123456789abcdef01234567",
		RawEventType: "Grab",
	}))
	assert.Empty(t, mapRepo.snapshot())
}

// TestMovieUseCase_Grabbed_NilMapRepo_NoWriteNoPanic — the nil-guard contract
// that keeps the three pre-B1.2 MovieDeps{} literals in this file (and any
// minimal wiring) alive: an unwired TorrentMovieMap is a silent no-op, not a
// nil-pointer panic. Tx is nil too, exercising the direct (non-tx) branch.
func TestMovieUseCase_Grabbed_NilMapRepo_NoWriteNoPanic(t *testing.T) {
	t.Parallel()
	uc := NewMovieUseCase(MovieDeps{Logger: quietLogger()})

	require.NotPanics(t, func() {
		require.NoError(t, uc.Process(context.Background(), domainwebhook.MovieEvent{
			Type:          domainwebhook.MovieEventTypeGrabbed,
			InstanceName:  "radarr-main",
			RadarrMovieID: 42,
			DownloadID:    "0123456789abcdef0123456789abcdef01234567",
			RawEventType:  "Grab",
		}))
	})
}

// TestMovieUseCase_Grabbed_NilTx_WritesDirectly — with no Transactor the work
// closure runs on the ambient ctx (mirror of handleGrabbed's tx-or-direct
// split), so the bridge row still lands.
func TestMovieUseCase_Grabbed_NilTx_WritesDirectly(t *testing.T) {
	t.Parallel()
	mapRepo := &fakeTorrentMovieMap{}
	uc := NewMovieUseCase(MovieDeps{TorrentMovieMap: mapRepo, Logger: quietLogger()})

	require.NoError(t, uc.Process(context.Background(), domainwebhook.MovieEvent{
		Type:          domainwebhook.MovieEventTypeGrabbed,
		InstanceName:  "radarr-main",
		RadarrMovieID: 42,
		DownloadID:    "0123456789abcdef0123456789abcdef01234567",
		RawEventType:  "Grab",
	}))
	require.Len(t, mapRepo.snapshot(), 1)
}

// TestMovieUseCase_Grabbed_RepoErrorSwallowed — a DB failure on the bridge write
// is WARN-logged and swallowed (return nil) per this usecase's fail-soft
// convention: Radarr must not retry, and B1.3 backfills the row.
func TestMovieUseCase_Grabbed_RepoErrorSwallowed(t *testing.T) {
	t.Parallel()
	mapRepo := &fakeTorrentMovieMap{err: ports.ErrDBUnavailable}
	tx := &fakeTransactor{}
	uc := NewMovieUseCase(MovieDeps{
		TorrentMovieMap: mapRepo,
		Tx:              tx,
		Logger:          quietLogger(),
	})

	err := uc.Process(context.Background(), domainwebhook.MovieEvent{
		Type:          domainwebhook.MovieEventTypeGrabbed,
		InstanceName:  "radarr-main",
		RadarrMovieID: 42,
		DownloadID:    "0123456789abcdef0123456789abcdef01234567",
		RawEventType:  "Grab",
	})
	require.NoError(t, err, "bridge failure must still answer 200")
	assert.Equal(t, 1, tx.called)
	assert.False(t, tx.committed, "the tx must have rolled back")
}

// TestMovieUseCase_Upsert_DoesNotWriteMovieMap — routing regression guard:
// MovieAdded/Download still go to handleUpsert (cache written) and NEVER to
// handleMovieGrabbed, even when a downloadId is somehow present.
func TestMovieUseCase_Upsert_DoesNotWriteMovieMap(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seedArrInstanceRow(t, db, "radarr-main")

			states := catalogpersistence.NewMovieStatesRepository(db)
			mapRepo := &fakeTorrentMovieMap{}
			uc := NewMovieUseCase(MovieDeps{
				Movies:          enrichpersistence.NewMovieRepository(db),
				States:          stubStateAdapter{states},
				SoftDeleter:     states,
				TorrentMovieMap: mapRepo,
				Tx:              catalogpersistence.NewGormTransactor(db),
				Logger:          quietLogger(),
			})

			require.NoError(t, uc.Process(ctx, domainwebhook.MovieEvent{
				Type:          domainwebhook.MovieEventTypeUpsert,
				InstanceName:  "radarr-main",
				RadarrMovieID: 42,
				Title:         "Dune",
				TMDBID:        438631,
				Monitored:     true,
				DownloadID:    "0123456789abcdef0123456789abcdef01234567",
				RawEventType:  "MovieAdded",
			}))

			// Cache path ran…
			st, err := states.Get(ctx, "radarr-main", 42)
			require.NoError(t, err)
			assert.True(t, st.IsActive())
			// …and the bridge did not.
			assert.Empty(t, mapRepo.snapshot(), "only Grab may write torrent_movie_map")
		})
	}
}
