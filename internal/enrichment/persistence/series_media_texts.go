package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesMediaTextsRepository persists the per-language poster/backdrop
// rows (series_media_texts, PK (series_id, language)). Variant A
// (Story 584). Mirrors SeriesTextsRepository but — like
// SeasonTextsRepository — COALESCE-protects EVERY content column
// (poster_asset, poster_hash, backdrop_asset, backdrop_hash) plus
// enriched_at, so a partial write (e.g. poster-only) never blanks a
// previously-fetched backdrop or freshness stamp.
type SeriesMediaTextsRepository struct {
	db *gorm.DB
}

func NewSeriesMediaTextsRepository(db *gorm.DB) *SeriesMediaTextsRepository {
	return &SeriesMediaTextsRepository{db: db}
}

// Get fetches the row for (series_id, language) exactly. Returns
// ports.ErrNotFound when no row matches.
func (r *SeriesMediaTextsRepository) Get(ctx context.Context, seriesID domain.SeriesID, language string) (series.SeriesMediaText, error) {
	var m database.SeriesMediaTextModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND language = ?", seriesID, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.SeriesMediaText{}, ports.ErrNotFound
		}
		return series.SeriesMediaText{}, fmt.Errorf("get series_media_texts: %w", err)
	}
	return toSeriesMediaText(m), nil
}

// GetWithFallback returns the row for the requested language, or the
// en-US fallback, or the first available row by language ascending
// (three-tier §5.6 CASE ORDER BY via pickLanguageFallback). ErrNotFound
// is the only NotFound sentinel — the composer's canon fallback (raw
// series.poster_asset) is the final, non-persistence tier.
func (r *SeriesMediaTextsRepository) GetWithFallback(ctx context.Context, seriesID domain.SeriesID, language string) (series.SeriesMediaText, error) {
	var m database.SeriesMediaTextModel
	if err := pickLanguageFallback(ctx, r.db, "series_media_texts", "series_id", int64(seriesID), language, &m); err != nil {
		return series.SeriesMediaText{}, err
	}
	if m.SeriesID == 0 {
		return series.SeriesMediaText{}, ports.ErrNotFound
	}
	return toSeriesMediaText(m), nil
}

// GetBackdropAnyLang returns the best per-COLUMN backdrop path for a series
// across ALL languages, preferring preferLang, then en-US, then any other
// language (deterministic language ASC). Unlike GetWithFallback — which picks
// ONE best-language ROW and then reads whatever backdrop_asset that row happens
// to carry (NULL for a poster-only row) — this scans only rows whose
// backdrop_asset is non-NULL, so a poster-only requested-language row never
// shadows another language's row that DOES have a backdrop (W18-15). Mirrors the
// per-column any-lang subselect the discovery list/search projections already
// use (list_repository.go / search.go). Returns (nil, nil) when NO language row
// carries a backdrop — the composer then renders a placeholder.
func (r *SeriesMediaTextsRepository) GetBackdropAnyLang(ctx context.Context, seriesID domain.SeriesID, preferLang string) (*string, error) {
	if preferLang == "" {
		preferLang = fallbackLanguage
	}
	const q = "SELECT smt.backdrop_asset AS backdrop_asset " +
		"FROM series_media_texts smt " +
		"WHERE smt.series_id = ? AND smt.backdrop_asset IS NOT NULL AND smt.backdrop_asset <> '' " +
		"ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = ? THEN 1 ELSE 0 END DESC, smt.language ASC " +
		"LIMIT 1"
	var row struct {
		BackdropAsset *string `gorm:"column:backdrop_asset"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, int64(seriesID), preferLang, fallbackLanguage).
		Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("get backdrop any-lang (series_media_texts): %w", err)
	}
	return row.BackdropAsset, nil
}

// GetPosterAnyLang returns the best per-COLUMN poster path across ALL languages
// that actually carry one, preferring preferLang → en-US → any (deterministic
// language ASC). Story 1081a: the hero calls this for the CONFIRMED-ABSENT case
// (requested-lang poster_asset NULL & poster_checked_at SET) to serve the stable
// original/canonical poster instead of a monogram — because we KNOW the localized
// poster will not arrive until a re-check, the original never gets swapped out.
// Rows whose poster_asset is NULL or empty are filtered, so the confirmed-absent
// requested-lang row never shadows another language's real poster. Returns
// (nil, nil) when NO language row carries a poster (truly art-less series →
// monogram). Mirrors GetBackdropAnyLang.
func (r *SeriesMediaTextsRepository) GetPosterAnyLang(ctx context.Context, seriesID domain.SeriesID, preferLang string) (*string, error) {
	if preferLang == "" {
		preferLang = fallbackLanguage
	}
	const q = "SELECT smt.poster_asset AS poster_asset " +
		"FROM series_media_texts smt " +
		"WHERE smt.series_id = ? AND smt.poster_asset IS NOT NULL AND smt.poster_asset <> '' " +
		"ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = ? THEN 1 ELSE 0 END DESC, smt.language ASC " +
		"LIMIT 1"
	var row struct {
		PosterAsset *string `gorm:"column:poster_asset"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, int64(seriesID), preferLang, fallbackLanguage).
		Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("get poster any-lang (series_media_texts): %w", err)
	}
	return row.PosterAsset, nil
}

// PosterMarker reports the per-locale poster presence state the freshener uses
// to decide an absent-recheck. Returns (absent, checkedAt): absent == true when
// the (series_id, language) row exists with poster_asset NULL/empty AND
// poster_checked_at SET (confirmed-absent); checkedAt is that stamp. A present
// poster, a never-checked row, or no row at all → (false, nil). Story 1081b.
func (r *SeriesMediaTextsRepository) PosterMarker(ctx context.Context, seriesID domain.SeriesID, language string) (bool, *time.Time, error) {
	var m database.SeriesMediaTextModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND language = ?", seriesID, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil, nil // no row → never checked
		}
		return false, nil, fmt.Errorf("poster marker series_media_texts: %w", err)
	}
	absent := (m.PosterAsset == nil || *m.PosterAsset == "") && m.PosterCheckedAt != nil
	if !absent {
		return false, nil, nil
	}
	return true, m.PosterCheckedAt, nil
}

// ListByIDsWithFallback returns one SeriesMediaText per requested
// series_id, applying a POSTER-PRESENCE ladder (requested language → en-US →
// any-available-language) in at most three round-trips. Used by the
// recommendations read path (584b), the grid localizer (584b), the person
// credits page and the TMDB-fallback recs.
//
// Poster-presence semantics (Story 1110) — aligned with GetPosterAnyLang
// (WHERE poster_asset IS NOT NULL AND <> ”): a tier only SATISFIES an id when
// the chosen row carries a NON-EMPTY poster_asset. A "confirmed-absent" row
// (Story 1081b: poster_asset NULL/empty + poster_checked_at SET) is a terminal
// per-locale state; it must NOT shadow a lower-tier row that HAS a real poster.
// The old row-presence ladder let a ru-RU confirmed-absent row block the en-US
// poster, stranding the card on the missing-art sentinel forever.
//
// Semantics:
//   - Empty seriesIDs slice → empty map, nil error (no SQL).
//   - lang == "" normalises to en-US.
//   - Requested-lang row WITH a poster        → entry uses that row (satisfied).
//   - Requested-lang row WITHOUT a poster     → seeded (never dropped), but the
//     id stays UNSATISFIED so en-US / any-lang can still supply a poster.
//   - en-US / any-lang row WITH a poster      → OVERWRITES the seeded row and
//     satisfies the id. A poster-less row of a lower tier only seeds an id that
//     is otherwise absent from the map (never downgrades a seeded row).
//   - No poster in ANY language               → the id keeps its best-available
//     (poster-less) row — its backdrop, if any, survives — and the caller
//     renders the sentinel legitimately.
//   - Zero rows for an id                     → key absent (caller keeps canon).
//
// NOTE: no consumer reads BackdropAsset off this result (all four read only
// PosterAsset), so the poster-first overwrite cannot regress a backdrop; the
// series-detail hero backdrop uses GetBackdropAnyLang.
func (r *SeriesMediaTextsRepository) ListByIDsWithFallback(
	ctx context.Context,
	seriesIDs []domain.SeriesID,
	lang string,
) (map[domain.SeriesID]series.SeriesMediaText, error) {
	if len(seriesIDs) == 0 {
		return map[domain.SeriesID]series.SeriesMediaText{}, nil
	}
	if lang == "" {
		lang = fallbackLanguage
	}
	ids := make([]int64, len(seriesIDs))
	for i, id := range seriesIDs {
		ids[i] = int64(id)
	}

	out := make(map[domain.SeriesID]series.SeriesMediaText, len(seriesIDs))
	// satisfied[id] is set only once out[id] carries a NON-EMPTY poster. A
	// seeded backdrop-only / confirmed-absent row leaves the id UNSATISFIED so a
	// lower tier can still supply a poster-bearing row.
	satisfied := make(map[domain.SeriesID]struct{}, len(seriesIDs))

	// Primary pass (requested lang). Seed EVERY row so a backdrop-only row is
	// never lost; mark satisfied only when the row carries a poster.
	var primary []database.SeriesMediaTextModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id IN ? AND language = ?", ids, lang).
		Find(&primary).Error; err != nil {
		return nil, fmt.Errorf("list series_media_texts by ids (lang=%s): %w", lang, err)
	}
	for _, m := range primary {
		t := toSeriesMediaText(m)
		out[t.SeriesID] = t
		if hasPoster(t) {
			satisfied[t.SeriesID] = struct{}{}
		}
	}

	// en-US pass (skipped when lang == en-US). remaining = ids NOT satisfied
	// (absent OR seeded-without-poster). A poster-bearing en-US row overwrites +
	// satisfies; a poster-less en-US row only seeds an id still absent from out.
	if lang != fallbackLanguage {
		remaining := unsatisfiedIDs(ids, satisfied)
		if len(remaining) > 0 {
			var fallback []database.SeriesMediaTextModel
			if err := dbFromContext(ctx, r.db).WithContext(ctx).
				Where("series_id IN ? AND language = ?", remaining, fallbackLanguage).
				Find(&fallback).Error; err != nil {
				return nil, fmt.Errorf("list series_media_texts en-US fallback: %w", err)
			}
			for _, m := range fallback {
				t := toSeriesMediaText(m)
				if hasPoster(t) {
					out[t.SeriesID] = t
					satisfied[t.SeriesID] = struct{}{}
				} else if _, ok := out[t.SeriesID]; !ok {
					out[t.SeriesID] = t
				}
			}
		}
	}

	// Any-lang pass. stillMissing = ids NOT satisfied. ORDER BY language ASC
	// makes the pick deterministic; the FIRST poster-bearing row seen per id
	// wins. A poster-less row only seeds an id still entirely absent from out.
	stillMissing := unsatisfiedIDs(ids, satisfied)
	if len(stillMissing) > 0 {
		var anyLang []database.SeriesMediaTextModel
		if err := dbFromContext(ctx, r.db).WithContext(ctx).
			Where("series_id IN ?", stillMissing).
			Order("language ASC").
			Find(&anyLang).Error; err != nil {
			return nil, fmt.Errorf("list series_media_texts any-lang fallback: %w", err)
		}
		for _, m := range anyLang {
			t := toSeriesMediaText(m)
			if _, done := satisfied[t.SeriesID]; done {
				continue
			}
			if hasPoster(t) {
				out[t.SeriesID] = t
				satisfied[t.SeriesID] = struct{}{}
			} else if _, ok := out[t.SeriesID]; !ok {
				out[t.SeriesID] = t
			}
		}
	}
	return out, nil
}

// hasPoster reports whether a media row carries a usable poster_asset
// (non-nil AND non-empty) — the poster-presence predicate the batch ladder
// keys on, matching GetPosterAnyLang's WHERE poster_asset IS NOT NULL AND <> ”.
func hasPoster(t series.SeriesMediaText) bool {
	return t.PosterAsset != nil && *t.PosterAsset != ""
}

// unsatisfiedIDs returns the ids whose poster is not yet satisfied (absent from
// the satisfied set), preserving input order for deterministic query params.
func unsatisfiedIDs(ids []int64, satisfied map[domain.SeriesID]struct{}) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := satisfied[domain.SeriesID(id)]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// Upsert writes a per-language media row by composite PK. Idempotent.
// Rejects a zero series_id or empty language. COALESCE shields all four
// media columns + enriched_at so a partial write never blanks a
// previously stored value (memory seasonfill-upsert-coalesce-pattern:
// bare excluded orphan branches trip SQLSTATE 42601 on Postgres — every
// DO UPDATE column is explicitly COALESCE-wrapped). updated_at always
// takes the new value.
func (r *SeriesMediaTextsRepository) Upsert(ctx context.Context, t series.SeriesMediaText) error {
	if t.SeriesID == 0 {
		return fmt.Errorf("upsert series_media_texts: series_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("upsert series_media_texts: language must be non-empty")
	}
	t.UpdatedAt = time.Now().UTC()
	m := fromSeriesMediaText(t)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "language"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"poster_asset":   gorm.Expr("COALESCE(excluded.poster_asset, series_media_texts.poster_asset)"),
			"poster_hash":    gorm.Expr("COALESCE(excluded.poster_hash, series_media_texts.poster_hash)"),
			"backdrop_asset": gorm.Expr("COALESCE(excluded.backdrop_asset, series_media_texts.backdrop_asset)"),
			"backdrop_hash":  gorm.Expr("COALESCE(excluded.backdrop_hash, series_media_texts.backdrop_hash)"),
			"enriched_at":    gorm.Expr("COALESCE(excluded.enriched_at, series_media_texts.enriched_at)"),
			// Story 1081a — DELIBERATE COALESCE EXCEPTION. The *_checked_at
			// presence markers are written PLAIN (excluded.X, not COALESCE) so a
			// re-check ALWAYS refreshes the stamp to the new "now" — a
			// confirmed-absent marker must not be frozen. INVARIANT (story §0.0):
			// every caller that writes a poster/backdrop asset here ALSO stamps
			// *_checked_at=now (writers 1/2/3 are strict-non-base + stamp; the
			// text-only RefreshSeriesText path no longer calls this Upsert at all),
			// so plain-excluded never receives a nil that would erase a valid marker.
			"poster_checked_at":   gorm.Expr("excluded.poster_checked_at"),
			"backdrop_checked_at": gorm.Expr("excluded.backdrop_checked_at"),
			"updated_at":          gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert series_media_texts: %w", err)
	}
	return nil
}

// InsertIfAbsent inserts a series_media_texts row ONLY when no row exists
// for (series_id, language) — INSERT … ON CONFLICT DO NOTHING. Mirrors
// SeriesTextsRepository.InsertBaseLangIfAbsent. W15-6: the discovery
// stub-upsert seeds the per-language poster/backdrop the TMDB list
// response already carried so cards render art before enrichment runs,
// WITHOUT ever clobbering a later RefreshAllLangs poster (its resolved
// hash) via a re-EnsureStub. Idempotent: re-running against an existing
// row is a no-op (0 rows affected, nil error).
func (r *SeriesMediaTextsRepository) InsertIfAbsent(ctx context.Context, t series.SeriesMediaText) error {
	if t.SeriesID == 0 {
		return fmt.Errorf("insert series_media_texts if absent: series_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("insert series_media_texts if absent: language must be non-empty")
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now().UTC()
	}
	m := fromSeriesMediaText(t)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "language"},
		},
		DoNothing: true,
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("insert series_media_texts if absent: %w", err)
	}
	return nil
}

func toSeriesMediaText(m database.SeriesMediaTextModel) series.SeriesMediaText {
	return series.SeriesMediaText{
		SeriesID:          m.SeriesID,
		Language:          m.Language,
		PosterAsset:       m.PosterAsset,
		PosterHash:        m.PosterHash,
		BackdropAsset:     m.BackdropAsset,
		BackdropHash:      m.BackdropHash,
		EnrichedAt:        m.EnrichedAt,
		PosterCheckedAt:   m.PosterCheckedAt,
		BackdropCheckedAt: m.BackdropCheckedAt,
		UpdatedAt:         m.UpdatedAt,
	}
}

func fromSeriesMediaText(t series.SeriesMediaText) database.SeriesMediaTextModel {
	return database.SeriesMediaTextModel{
		SeriesID:          t.SeriesID,
		Language:          t.Language,
		PosterAsset:       t.PosterAsset,
		PosterHash:        t.PosterHash,
		BackdropAsset:     t.BackdropAsset,
		BackdropHash:      t.BackdropHash,
		EnrichedAt:        t.EnrichedAt,
		PosterCheckedAt:   t.PosterCheckedAt,
		BackdropCheckedAt: t.BackdropCheckedAt,
		UpdatedAt:         t.UpdatedAt,
	}
}
