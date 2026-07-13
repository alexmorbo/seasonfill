package persistence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// personCreditKey is the in-memory mirror of the
// `person_credits_credit` UNIQUE index `(person_id, tmdb_credit_id)`.
// Used by batchUpsert's dedupe pass to fold duplicates before they
// reach Postgres — see SQLSTATE 21000 below.
type personCreditKey struct {
	personID     int64
	tmdbCreditID string
}

// PersonCredit is the read-shape returned by PersonCreditsRepository.
// PosterPath is an upstream TMDB image path string in v1 — the media
// downloader picks it up lazily on person-page open.
type PersonCredit = database.PersonCreditModel

// PersonCreditsRepository persists the `person_credits` table (PRD
// §5.3 row "person_credits"). Natural key
// (person_id, tmdb_credit_id) — TMDB
// /person/{id}/tv_credits + /movie_credits is the dominant write
// source, so BatchUpsert is the primary write path (one INSERT … ON
// CONFLICT round-trip for N rows).
//
// ListByPerson covers the person-page full filmography list;
// ListByMedia covers the reverse lookup "who from my library
// appears in this TMDB title?" on the person page's "More library
// credits" list.
type PersonCreditsRepository struct {
	db *gorm.DB
}

func NewPersonCreditsRepository(db *gorm.DB) *PersonCreditsRepository {
	return &PersonCreditsRepository{db: db}
}

// Get fetches by primary key. Returns ports.ErrNotFound on miss.
func (r *PersonCreditsRepository) Get(ctx context.Context, id int64) (PersonCredit, error) {
	var m database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PersonCredit{}, ports.ErrNotFound
		}
		return PersonCredit{}, fmt.Errorf("get person_credit: %w", err)
	}
	return m, nil
}

// ListByPerson returns every credit row for personID, ordered by
// (year DESC NULLS LAST, title ASC). PRD §5.6 read path: the H-1
// person page sorts library credits by "recent" (last_aired_at) and
// other_credits by year/title; the year DESC ordering is the
// closest cheap approximation against TMDB-only data.
func (r *PersonCreditsRepository) ListByPerson(ctx context.Context, personID int64) ([]PersonCredit, error) {
	var models []database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("person_id = ?", personID).
		Order("year DESC, title ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list person_credits by person: %w", err)
	}
	return models, nil
}

// ListByPersons is the batched sibling of ListByPerson: it returns every credit
// row for the given personIDs in ONE `person_id IN (?)` query, grouped into a map
// keyed by person_id. Within each person the rows keep ListByPerson's
// (year DESC NULLS LAST, title ASC) ordering. A personID with no credits is
// simply absent from the map. Empty input returns an empty map + nil (no query).
// De-dupes personIDs (and drops zero ids) before the IN clause.
//
// Story 1070 — collapses the cast composer's in_library-probe Pass 1 N+1 (one
// ListByPerson per unique person) into a single round-trip.
func (r *PersonCreditsRepository) ListByPersons(ctx context.Context, personIDs []int64) (map[int64][]PersonCredit, error) {
	out := make(map[int64][]PersonCredit, len(personIDs))
	seen := make(map[int64]struct{}, len(personIDs))
	deduped := make([]int64, 0, len(personIDs))
	for _, id := range personIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		deduped = append(deduped, id)
	}
	if len(deduped) == 0 {
		return out, nil
	}
	var models []database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("person_id IN ?", deduped).
		Order("person_id ASC, year DESC, title ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list person_credits by persons: %w", err)
	}
	for _, m := range models {
		out[m.PersonID] = append(out[m.PersonID], m)
	}
	return out, nil
}

// ListByPersonWithTextFallback returns every credit row for personID with
// character_name resolved per S-G: requested language → en-US → base
// person_credits.character_name. Mirrors ListByMediaWithTextFallback (two
// LEFT JOINs on person_credits_texts + COALESCE, one round-trip) but is
// scoped by person_id and preserves ListByPerson's (year DESC, title ASC)
// ordering so the person-page filmography order is unchanged. lang=="" defaults
// to en-US, collapsing both joins onto the base tier.
func (r *PersonCreditsRepository) ListByPersonWithTextFallback(
	ctx context.Context,
	personID int64,
	lang string,
) ([]PersonCredit, error) {
	if lang == "" {
		lang = fallbackLanguage
	}
	const q = `
SELECT
  pc.id, pc.person_id, pc.tmdb_credit_id, pc.media_type, pc.tmdb_media_id,
  pc.title, pc.original_title, pc.year,
  COALESCE(t_req.character_name, t_base.character_name, pc.character_name) AS character_name,
  pc.kind, pc.department, pc.job, pc.poster_path, pc.vote_average,
  pc.tmdb_votes, pc.episode_count, pc.credit_order, pc.last_appearance_season, pc.created_at, pc.updated_at
FROM person_credits pc
LEFT JOIN person_credits_texts t_req
  ON t_req.person_credit_id = pc.id AND t_req.language = ?
LEFT JOIN person_credits_texts t_base
  ON t_base.person_credit_id = pc.id AND t_base.language = ?
WHERE pc.person_id = ?
ORDER BY pc.year DESC, pc.title ASC`
	var models []database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, lang, fallbackLanguage, personID).
		Scan(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list person_credits by person (i18n): %w", err)
	}
	return models, nil
}

// ListByMedia returns every credit row for the given media
// (media_type, tmdb_media_id) — the reverse lookup that powers
// "who from my library appears in this TMDB title?". Hits the
// `person_credits_media` index.
func (r *PersonCreditsRepository) ListByMedia(ctx context.Context, mediaType string, tmdbMediaID int) ([]PersonCredit, error) {
	if mediaType == "" {
		return nil, fmt.Errorf("list person_credits by media: media_type must be non-empty")
	}
	var models []database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("media_type = ? AND tmdb_media_id = ?", mediaType, tmdbMediaID).
		Order("person_id ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list person_credits by media: %w", err)
	}
	return models, nil
}

// ListByMediaWithTextFallback returns every credit row for (media_type,
// tmdb_media_id) with character_name resolved per S-G: requested language
// → en-US → base person_credits.character_name. Two LEFT JOINs against
// person_credits_texts + COALESCE, one round-trip. Ordered by person_id ASC
// (same as ListByMedia — the cast read path relies on it). lang=="" defaults
// to en-US, collapsing both joins onto the base tier.
func (r *PersonCreditsRepository) ListByMediaWithTextFallback(
	ctx context.Context,
	mediaType string,
	tmdbMediaID int,
	lang string,
) ([]PersonCredit, error) {
	if mediaType == "" {
		return nil, fmt.Errorf("list person_credits by media (i18n): media_type must be non-empty")
	}
	if lang == "" {
		lang = fallbackLanguage
	}
	const q = `
SELECT
  pc.id, pc.person_id, pc.tmdb_credit_id, pc.media_type, pc.tmdb_media_id,
  pc.title, pc.original_title, pc.year,
  COALESCE(t_req.character_name, t_base.character_name, pc.character_name) AS character_name,
  pc.kind, pc.department, pc.job, pc.poster_path, pc.vote_average,
  pc.tmdb_votes, pc.episode_count, pc.credit_order, pc.last_appearance_season, pc.created_at, pc.updated_at
FROM person_credits pc
LEFT JOIN person_credits_texts t_req
  ON t_req.person_credit_id = pc.id AND t_req.language = ?
LEFT JOIN person_credits_texts t_base
  ON t_base.person_credit_id = pc.id AND t_base.language = ?
WHERE pc.media_type = ? AND pc.tmdb_media_id = ?
ORDER BY pc.person_id ASC`
	var models []database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, lang, fallbackLanguage, mediaType, tmdbMediaID).
		Scan(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list person_credits by media (i18n): %w", err)
	}
	return models, nil
}

// Upsert writes one credit row by natural key. Idempotent.
func (r *PersonCreditsRepository) Upsert(ctx context.Context, pc PersonCredit) (int64, error) {
	ids, err := r.batchUpsert(ctx, []PersonCredit{pc})
	if err != nil {
		return 0, err
	}
	if len(ids) != 1 {
		return 0, fmt.Errorf("upsert person_credit: expected 1 id, got %d", len(ids))
	}
	return ids[0], nil
}

// BatchUpsert writes N credit rows in ONE INSERT … ON CONFLICT
// round-trip. Returned slice mirrors input order; index i carries
// the assigned id for input i. Empty input returns empty slice + nil.
// The conflict target (person_id, tmdb_credit_id) MUST match the UQ
// index `person_credits_credit` exactly — re-running TMDB
// /person/{id}/tv_credits never duplicates.
func (r *PersonCreditsRepository) BatchUpsert(ctx context.Context, credits []PersonCredit) ([]int64, error) {
	return r.batchUpsert(ctx, credits)
}

func (r *PersonCreditsRepository) batchUpsert(ctx context.Context, credits []PersonCredit) ([]int64, error) {
	if len(credits) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()

	// Per-row validation runs BEFORE dedupe — a malformed row must
	// surface its error rather than be silently swallowed by a
	// duplicate-key drop.
	validated := make([]database.PersonCreditModel, 0, len(credits))
	for _, c := range credits {
		if c.PersonID == 0 {
			return nil, fmt.Errorf("upsert person_credit: person_id must be non-zero")
		}
		if c.TMDBCreditID == "" {
			return nil, fmt.Errorf("upsert person_credit: tmdb_credit_id must be non-empty")
		}
		if c.MediaType == "" {
			return nil, fmt.Errorf("upsert person_credit: media_type must be non-empty")
		}
		if c.TMDBMediaID == 0 {
			return nil, fmt.Errorf("upsert person_credit: tmdb_media_id must be non-zero")
		}
		if c.Title == "" {
			return nil, fmt.Errorf("upsert person_credit: title must be non-empty")
		}
		if c.Kind == "" {
			return nil, fmt.Errorf("upsert person_credit: kind must be non-empty")
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		c.UpdatedAt = now
		validated = append(validated, c)
	}

	// Dedupe by the (person_id, tmdb_credit_id) conflict target.
	// Postgres rejects `INSERT … ON CONFLICT DO UPDATE` when two input
	// rows hit the same target row (SQLSTATE 21000:
	// "ON CONFLICT DO UPDATE command cannot affect row a second
	// time"). The common producer is series_worker.applyEpisodeCredits,
	// which fans a single crew credit_id (show-runner / EP / director)
	// across every episode of a season.
	//
	// Semantics: keep the first occurrence per key. `firstIdx` maps key
	// to its index in `models` (the INSERT slice). Duplicate inputs at
	// later positions are dropped from the INSERT but the returned
	// `ids` slice still mirrors the input length — duplicates resolve
	// to the same DB id their first-seen sibling earns.
	firstIdx := make(map[personCreditKey]int, len(validated))
	models := make([]database.PersonCreditModel, 0, len(validated))
	// inputToModel[i] = position in `models` that input row i maps to.
	inputToModel := make([]int, len(validated))
	dropped := 0
	for i, c := range validated {
		key := personCreditKey{personID: c.PersonID, tmdbCreditID: c.TMDBCreditID}
		if idx, ok := firstIdx[key]; ok {
			inputToModel[i] = idx
			dropped++
			continue
		}
		idx := len(models)
		firstIdx[key] = idx
		inputToModel[i] = idx
		models = append(models, c)
	}

	if dropped > 0 {
		slog.DebugContext(ctx, "person_credits.dedupe",
			slog.String("domain", "enrichment"),
			slog.Int("input", len(validated)),
			slog.Int("kept", len(models)),
			slog.Int("dropped", dropped),
		)
	}

	// Batch in chunks of 1000 to stay under Postgres' 65535 extended-protocol
	// parameter cap. PersonCreditModel has ~18 bindable columns, so the
	// hard ceiling is ~3640 rows/round-trip; 1000 picks a safe margin and
	// keeps the OnConflict clause + RETURNING id round-tripping per batch.
	// Producer in prod: Discovery worker's enrichment dispatcher fans
	// TMDB /person/{id}/tv_credits across rich casts (Rick and Morty et al.)
	// and easily exceeded the 3640-row ceiling pre-batching (B-19 follow-up).
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "person_id"},
			{Name: "tmdb_credit_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"media_type":    gorm.Expr("excluded.media_type"),
			"tmdb_media_id": gorm.Expr("excluded.tmdb_media_id"),
			"title":         gorm.Expr("excluded.title"),
			// Story 1126 — COALESCE-guard original_title/year. Identical clobber
			// shape to poster_path/tmdb_votes: the person-worker build
			// (personCreditFromTV/Movie) populates both — OriginalTitle via
			// nonEmptyPtr(c.OriginalName/OriginalTitle) and Year via
			// yearFromReleaseDate — while the series-worker build
			// (mapSeriesCreditsToPersonCredits) has NO source at its seam and
			// emits NULL for both (first_air_date isn't in the patch shape). A
			// series-worker upsert landing AFTER the person-worker populated them
			// must not null them back out — the H-1 person page reads both
			// (SELECT pc.original_title, pc.year). COALESCE keeps legitimate
			// updates intact: a non-NULL excluded still overwrites; only a NULL
			// excluded is ignored.
			"original_title": gorm.Expr("COALESCE(excluded.original_title, person_credits.original_title)"),
			"year":           gorm.Expr("COALESCE(excluded.year, person_credits.year)"),
			"character_name": gorm.Expr("excluded.character_name"),
			"kind":           gorm.Expr("excluded.kind"),
			"department":     gorm.Expr("excluded.department"),
			"job":            gorm.Expr("excluded.job"),
			// Story 1126 — COALESCE-guard the credit poster path. Same clobber
			// shape as vote_average: the series-worker person_credits(tv) build
			// (mapSeriesCreditsToPersonCredits) has NO poster source at its seam
			// and emits NULL, while the person-worker /person/{id}/tv_credits +
			// /movie_credits build populates it (personCreditFromTV/Movie →
			// nonEmptyPtr(c.PosterPath)). A series-worker upsert landing AFTER the
			// person-worker populated poster_path must not null it back out and
			// blank the person-page filmography cards. COALESCE keeps legitimate
			// updates intact — a non-NULL excluded (a fresh/localised person-worker
			// poster) still overwrites; only a NULL excluded is ignored.
			"poster_path": gorm.Expr("COALESCE(excluded.poster_path, person_credits.poster_path)"),
			// Story 1034 — COALESCE-guard the TMDB show rating. The
			// series-worker person_credits(tv) write path
			// (mapSeriesCreditsToPersonCredits) is order-less on the rating
			// versus the person-worker /person/{id}/tv_credits write, and a
			// series-worker upsert that lands AFTER the person-worker
			// populated vote_average must not null it back out. Mirrors the
			// credit_order guard above — the H-1 person page "other credits"
			// ★rating reads this column (OtherCreditEntry.VoteAverage).
			"vote_average": gorm.Expr("COALESCE(excluded.vote_average, person_credits.vote_average)"),
			// Story 1126 — COALESCE-guard the TMDB show vote count. tmdb_votes is
			// the show-level vote_count paired with vote_average; the person-worker
			// populates it (personCreditFromTV/Movie → nonZeroIntPtr(c.VoteCount))
			// while the series-worker historically left it NULL, so a later
			// series-worker upsert nulled the person-worker value. Mirrors the
			// vote_average guard above; the series-worker now also populates it
			// from tv.VoteCount (belt-and-suspenders, either alone heals the card).
			"tmdb_votes":    gorm.Expr("COALESCE(excluded.tmdb_votes, person_credits.tmdb_votes)"),
			"episode_count": gorm.Expr("excluded.episode_count"),
			// Story 1087b — COALESCE-guard billing order: the series-worker
			// aggregate_credits write is the ONLY source of credit_order; a
			// later person-worker tv_credits write (order-less) on the same
			// (person_id, tmdb_credit_id) must not null it out.
			"credit_order": gorm.Expr("COALESCE(excluded.credit_order, person_credits.credit_order)"),
			// Story 1090 — MAX-merge last_appearance_season. Portable across
			// BOTH dialects (GREATEST is PG-only; SQLite scalar MAX(NULL,x)=NULL).
			// NULL-safe on both sides: a NULL excluded (staged path / non-cast
			// writer) preserves the stored value; a NULL stored takes excluded;
			// otherwise the greater wins. Never regresses a higher stored value.
			"last_appearance_season": gorm.Expr(
				"CASE " +
					"WHEN excluded.last_appearance_season IS NULL THEN person_credits.last_appearance_season " +
					"WHEN person_credits.last_appearance_season IS NULL THEN excluded.last_appearance_season " +
					"WHEN excluded.last_appearance_season > person_credits.last_appearance_season THEN excluded.last_appearance_season " +
					"ELSE person_credits.last_appearance_season END"),
			"updated_at": gorm.Expr("excluded.updated_at"),
		}),
	}).CreateInBatches(&models, 1000).Error
	if err != nil {
		return nil, fmt.Errorf("batch upsert person_credits: %w", err)
	}

	ids := make([]int64, len(validated))
	for i, idx := range inputToModel {
		ids[i] = models[idx].ID
	}
	return ids, nil
}
