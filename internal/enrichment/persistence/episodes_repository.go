package persistence

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// episodeBatchSize chunks BatchUpsert INSERT statements to stay under
// Postgres extended-protocol bind-parameter limit (65535). EpisodeModel
// has 17 GORM-bound columns; 1000 rows × 17 = 17000 params — 3.8× margin
// under the limit, survives ~13 future column additions without re-tuning.
// Long-running series (3855+ episodes) historically hit the limit before
// this chunking; see B-27 / story 475. Pattern mirrors qbit_torrents
// repository (`internal/catalog/persistence/qbit_torrents_repository.go`)
// which already uses CreateInBatches with the same OnConflict shape.
const episodeBatchSize = 1000

// EpisodesRepository persists the canonical `episodes` table. Natural
// key (series_id, season_number, episode_number) — TMDB / Sonarr both
// emit episode lists in batches, so BatchUpsert is the primary write
// path (one INSERT … ON CONFLICT round-trip for N rows).
type EpisodesRepository struct {
	db *gorm.DB
}

func NewEpisodesRepository(db *gorm.DB) *EpisodesRepository {
	return &EpisodesRepository{db: db}
}

// Get returns the canonical episode row by primary key. Missing row
// → typed EpisodeNotFoundError; F-2c-3 dropped the legacy
// errors.Join(typed, ports.ErrNotFound) shim. The method has no
// external callers; tests use errors.As to assert the typed sentinel.
func (r *EpisodesRepository) Get(ctx context.Context, id int64) (series.CanonEpisode, error) {
	var m database.EpisodeModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.CanonEpisode{}, &sharedErrors.EpisodeNotFoundError{ID: domain.EpisodeID(id)}
		}
		return series.CanonEpisode{}, fmt.Errorf("get episode: %w", err)
	}
	return toCanonEpisode(m), nil
}

func (r *EpisodesRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]series.CanonEpisode, error) {
	var models []database.EpisodeModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("season_number ASC, episode_number ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list episodes: %w", err)
	}
	out := make([]series.CanonEpisode, 0, len(models))
	for _, m := range models {
		out = append(out, toCanonEpisode(m))
	}
	return out, nil
}

func (r *EpisodesRepository) ListBySeason(ctx context.Context, seriesID domain.SeriesID, seasonNumber int) ([]series.CanonEpisode, error) {
	var models []database.EpisodeModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND season_number = ?", seriesID, seasonNumber).
		Order("episode_number ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list season episodes: %w", err)
	}
	out := make([]series.CanonEpisode, 0, len(models))
	for _, m := range models {
		out = append(out, toCanonEpisode(m))
	}
	return out, nil
}

// CountBySeries returns the count of episodes rows for seriesID.
// Used by the H-1 cast composer (Story 216) as the divisor for
// per-cast Main / Recurring / Guest derivation
// (episode_count / total_episode_count). Indexed via the natural
// key UQ `episodes_natural (series_id, season_number,
// episode_number)` — Postgres + sqlite both pick the leading
// column for the count.
func (r *EpisodesRepository) CountBySeries(ctx context.Context, seriesID domain.SeriesID) (int, error) {
	var n int64
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("episodes").
		Where("series_id = ?", seriesID).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count episodes by series: %w", err)
	}
	return int(n), nil
}

// AggregateBySeries returns the per-season episode rollup for a series in ONE
// GROUP BY query — episode_count + MIN/MAX(air_date). Used by the E-1 B3c
// SeasonsComposer to fill SeasonSummary.air_date_end (MAX; seasons has no
// air_date_end column) + episode_count without loading every episode row.
//
// Seasons with zero episode rows are simply absent from the map (the composer
// falls back to canon seasons.episode_count / seasons.air_date for those).
// MIN/MAX(air_date) over the *time.Time column is dialect-safe: Postgres uses
// native timestamp ordering, SQLite orders the GORM-serialised ISO-8601 text
// lexically (equivalent for zero-padded UTC) — the D-0 dual-backend test proves
// both. NULL air_date rows drop out of MIN/MAX per SQL semantics; a season whose
// episodes ALL have NULL air_date yields nil First/LastAirDate.
func (r *EpisodesRepository) AggregateBySeries(
	ctx context.Context,
	seriesID domain.SeriesID,
) (map[int]series.SeasonEpisodeAggregate, error) {
	type aggRow struct {
		SeasonNumber int
		EpisodeCount int
		// aggScanTime absorbs the dialect split: Postgres hands back a
		// time.Time from MIN/MAX(timestamp); SQLite strips the column
		// affinity of an aggregate and hands back the stored ISO-8601 text.
		FirstAirDate aggScanTime
		LastAirDate  aggScanTime
	}
	var rows []aggRow
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("episodes").
		Select("season_number AS season_number, "+
			"COUNT(*) AS episode_count, "+
			"MIN(air_date) AS first_air_date, "+
			"MAX(air_date) AS last_air_date").
		Where("series_id = ?", seriesID).
		Group("season_number").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("aggregate episodes by series: %w", err)
	}
	out := make(map[int]series.SeasonEpisodeAggregate, len(rows))
	for _, rw := range rows {
		out[rw.SeasonNumber] = series.SeasonEpisodeAggregate{
			SeasonNumber: rw.SeasonNumber,
			EpisodeCount: rw.EpisodeCount,
			FirstAirDate: rw.FirstAirDate.t,
			LastAirDate:  rw.LastAirDate.t,
		}
	}
	return out, nil
}

// aggScanTime is a dialect-safe sql.Scanner for MIN/MAX(air_date). Postgres
// returns a driver time.Time; the pure-Go SQLite driver drops the aggregate's
// column affinity and returns the stored text ("2006-01-02 15:04:05-07:00").
// database/sql will NOT parse a string into *time.Time, so a plain *time.Time
// scan target fails on SQLite — this type parses the known layouts instead.
// A nil / empty value (season with all-NULL air_date) yields a nil *time.Time.
type aggScanTime struct{ t *time.Time }

var aggScanTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02",
}

// Value satisfies driver.Valuer so GORM accepts aggScanTime as a scalar column
// target (it requires both Scanner AND Valuer). aggScanTime is read-only — it is
// never written back — so Value simply surfaces the held *time.Time.
func (a aggScanTime) Value() (driver.Value, error) {
	if a.t == nil {
		return nil, nil
	}
	return *a.t, nil
}

func (a *aggScanTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		a.t = nil
		return nil
	case time.Time:
		u := x.UTC()
		a.t = &u
		return nil
	case []byte:
		return a.Scan(string(x))
	case string:
		if x == "" {
			a.t = nil
			return nil
		}
		for _, layout := range aggScanTimeLayouts {
			if parsed, err := time.Parse(layout, x); err == nil {
				u := parsed.UTC()
				a.t = &u
				return nil
			}
		}
		return fmt.Errorf("aggregate air_date: cannot parse time %q", x)
	default:
		return fmt.Errorf("aggregate air_date: unsupported scan type %T", v)
	}
}

// Upsert writes one episode by natural key. Idempotent.
func (r *EpisodesRepository) Upsert(ctx context.Context, e series.CanonEpisode) (int64, error) {
	id, err := r.batchUpsert(ctx, []series.CanonEpisode{e})
	if err != nil {
		return 0, err
	}
	if len(id) != 1 {
		return 0, fmt.Errorf("upsert episode: expected 1 id, got %d", len(id))
	}
	return id[0], nil
}

// BatchUpsert writes N episodes in a single INSERT … ON CONFLICT
// statement (or two on the rare partition: GORM emits one batch per
// round). The returned slice mirrors the input order; index i carries
// the assigned id for input i. Empty input returns empty slice + nil.
func (r *EpisodesRepository) BatchUpsert(ctx context.Context, episodes []series.CanonEpisode) ([]int64, error) {
	return r.batchUpsert(ctx, episodes)
}

func (r *EpisodesRepository) batchUpsert(ctx context.Context, episodes []series.CanonEpisode) ([]int64, error) {
	if len(episodes) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	models := make([]database.EpisodeModel, 0, len(episodes))
	for _, e := range episodes {
		if e.SeriesID == 0 {
			return nil, fmt.Errorf("upsert episode: series_id must be non-zero")
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		e.UpdatedAt = now
		models = append(models, fromCanonEpisode(e))
	}
	// B-27: chunk via CreateInBatches to stay under Postgres 65535 bind-param
	// limit. For len(models) <= episodeBatchSize, GORM emits exactly one
	// INSERT — behavior identical to the previous .Create(&models) path.
	// For larger inputs, GORM emits ceil(N/batchSize) INSERTs, all inside
	// the caller's transaction (series_worker wraps applyAll in Transactor.
	// Transaction → atomicity preserved across chunks).
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "season_number"},
			{Name: "episode_number"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"season_id",
			"tmdb_episode_number", "tmdb_episode_id",
			"sonarr_episode_id", "absolute_number",
			"air_date", "runtime_minutes", "finale_type",
			"still_asset", "tmdb_rating", "tmdb_votes",
			"updated_at",
		}),
	}).CreateInBatches(&models, episodeBatchSize).Error
	if err != nil {
		return nil, fmt.Errorf("batch upsert episodes: %w", err)
	}
	ids := make([]int64, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids, nil
}

func toCanonEpisode(m database.EpisodeModel) series.CanonEpisode {
	return series.CanonEpisode{
		ID:                m.ID,
		SeriesID:          m.SeriesID,
		SeasonID:          m.SeasonID,
		SeasonNumber:      m.SeasonNumber,
		EpisodeNumber:     m.EpisodeNumber,
		TMDBEpisodeNumber: m.TMDBEpisodeNumber,
		TMDBEpisodeID:     m.TMDBEpisodeID,
		SonarrEpisodeID:   m.SonarrEpisodeID,
		AbsoluteNumber:    m.AbsoluteNumber,
		AirDate:           m.AirDate,
		RuntimeMinutes:    m.RuntimeMinutes,
		FinaleType:        m.FinaleType,
		StillAsset:        m.StillAsset,
		TMDBRating:        m.TMDBRating,
		TMDBVotes:         m.TMDBVotes,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}
}

func fromCanonEpisode(e series.CanonEpisode) database.EpisodeModel {
	return database.EpisodeModel{
		ID:                e.ID,
		SeriesID:          e.SeriesID,
		SeasonID:          e.SeasonID,
		SeasonNumber:      e.SeasonNumber,
		EpisodeNumber:     e.EpisodeNumber,
		TMDBEpisodeNumber: e.TMDBEpisodeNumber,
		TMDBEpisodeID:     e.TMDBEpisodeID,
		SonarrEpisodeID:   e.SonarrEpisodeID,
		AbsoluteNumber:    e.AbsoluteNumber,
		AirDate:           e.AirDate,
		RuntimeMinutes:    e.RuntimeMinutes,
		FinaleType:        e.FinaleType,
		StillAsset:        e.StillAsset,
		TMDBRating:        e.TMDBRating,
		TMDBVotes:         e.TMDBVotes,
		CreatedAt:         e.CreatedAt,
		UpdatedAt:         e.UpdatedAt,
	}
}
