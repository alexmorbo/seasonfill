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

// mediaTypeTVDiscriminator is the show-level media_type literal used by the
// F-10 identity CASE guard in personCreditsDoUpdates. It mirrors
// tmdb.MediaTypeTV ("tv") — the value both personCreditFromTV and the
// series-worker mapSeriesCreditsToPersonCredits actually stamp — but is
// declared locally so the persistence layer does not depend on the clients
// package (AUDIT-S3 clean-architecture: persistence must not import
// internal/shared/clients/tmdb).
const mediaTypeTVDiscriminator = "tv"

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

// Upsert writes one credit row by natural key. Idempotent. Uses the
// COALESCE-guarded (series-worker) discipline — single-row Upsert has no
// authoritative caller.
func (r *PersonCreditsRepository) Upsert(ctx context.Context, pc PersonCredit) (int64, error) {
	ids, err := r.batchUpsert(ctx, []PersonCredit{pc}, false)
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
//
// This is the COALESCE-guarded (series-worker) discipline: the six
// TMDB-owned columns (poster_path, original_title, year, vote_average,
// tmdb_votes, episode_count) are preserved against a NULL excluded, because
// the series-worker build (mapSeriesCreditsToPersonCredits / applyEpisodeCredits)
// carries only partial aggregate data and emits NULL for columns it has no
// source for at its seam (#1034 / #1126 / AUDIT-S3 F-06). The person-worker uses
// BatchUpsertAuthoritative instead.
func (r *PersonCreditsRepository) BatchUpsert(ctx context.Context, credits []PersonCredit) ([]int64, error) {
	return r.batchUpsert(ctx, credits, false)
}

// BatchUpsertAuthoritative is the PERSON-WORKER write path (AUDIT-S3 F-04). Same
// INSERT … ON CONFLICT round-trip as BatchUpsert, but the six TMDB-owned columns
// (poster_path, original_title, year, vote_average, tmdb_votes, episode_count)
// are written RAW (excluded.X) instead of COALESCE-guarded: the person worker
// fetches fresh full TMDB /person/{id}/tv_credits + /movie_credits for THIS
// credit, so a NULL excluded is a genuine TMDB withdrawal that must self-heal the
// column (go NULL) rather than be pinned to a stale stored value forever. The
// identity columns (media_type, tmdb_media_id) still carry the F-10 CASE guard.
func (r *PersonCreditsRepository) BatchUpsertAuthoritative(ctx context.Context, credits []PersonCredit) ([]int64, error) {
	return r.batchUpsert(ctx, credits, true)
}

func (r *PersonCreditsRepository) batchUpsert(ctx context.Context, credits []PersonCredit, authoritative bool) ([]int64, error) {
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
		DoUpdates: clause.Assignments(personCreditsDoUpdates(authoritative)),
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

// personCreditsDoUpdates builds the ON CONFLICT (person_id, tmdb_credit_id)
// DO UPDATE assignment map for person_credits. It is the SINGLE source of the
// upsert's column-merge policy, shared by BatchUpsert (authoritative=false) and
// BatchUpsertAuthoritative (authoritative=true) so the map is never duplicated.
//
// AUDIT-S3 F-04 / F-06 — the six TMDB-owned columns (poster_path,
// original_title, year, vote_average, tmdb_votes, episode_count) swap between
// two disciplines:
//
//   - authoritative=false — SERIES-WORKER seam. COALESCE-guarded
//     (COALESCE(excluded.X, person_credits.X)): mapSeriesCreditsToPersonCredits /
//     applyEpisodeCredits carry only partial aggregate data and emit NULL for
//     columns they have no source for at their seam, so a series-worker upsert
//     landing AFTER the person-worker populated them must NOT null them back out
//     (#1034 / #1126; episode_count is F-06 — a 0→NULL tv_credits refresh must
//     not null-clobber the aggregate total).
//
//   - authoritative=true — PERSON-WORKER seam. RAW (excluded.X): the person
//     worker fetches fresh full TMDB /person/{id}/tv_credits + /movie_credits for
//     THIS credit, so a NULL excluded is a genuine TMDB withdrawal that must
//     self-heal the column (go NULL) rather than pin the stale stored value
//     forever (F-04).
//
// AUDIT-S3 F-10 — media_type + tmdb_media_id carry a CASE guard in BOTH variants
// (identity logic, independent of the enrichment-column split). media_type is
// NOT part of the (person_id, tmdb_credit_id) unique key, so if an episode-crew
// credit_id ever equals an aggregate/'tv' credit_id for the same person, a naive
// RAW write would flip the stored media_type between 'tv' and 'tv_episode' and
// ListByMedia('tv', showID) would then miss the row. The guard pins a show-level
// 'tv' value: whenever EITHER side is 'tv' the row stays 'tv', and tmdb_media_id
// tracks the SAME 'tv' side so the pair never desyncs. Behaviour-preserving for
// the normal non-colliding case: a plain 'tv' row stays 'tv', a plain
// 'tv_episode' row stays 'tv_episode', a 'movie' row stays 'movie'.
//
// All SQL is dialect-portable (SQLite UPSERT + Postgres ON CONFLICT both accept
// the `excluded` pseudo-table and the `person_credits` target-table reference;
// no GREATEST / PG-only cast). Proven on both backends by the existing
// last_appearance_season MAX-merge test.
func personCreditsDoUpdates(authoritative bool) map[string]any {
	// enrich selects RAW vs COALESCE for the six TMDB-owned columns.
	enrich := func(name string) clause.Expr {
		if authoritative {
			return gorm.Expr("excluded." + name)
		}
		return gorm.Expr("COALESCE(excluded." + name + ", person_credits." + name + ")")
	}
	// tvLit is the single-quoted SQL literal for the show-level 'tv'
	// discriminator, derived from mediaTypeTVDiscriminator (mirrors
	// tmdb.MediaTypeTV) so it never drifts from the value the writers actually
	// store (mappers.go personCreditFromTV + series_worker
	// mapSeriesCreditsToPersonCredits both stamp tmdb.MediaTypeTV).
	tvLit := fmt.Sprintf("'%s'", mediaTypeTVDiscriminator) // "'tv'"

	return map[string]any{
		// F-10 — pin 'tv' whenever either side is 'tv'; else keep excluded's value.
		"media_type": gorm.Expr(fmt.Sprintf(
			"CASE WHEN excluded.media_type = %[1]s OR person_credits.media_type = %[1]s "+
				"THEN %[1]s ELSE excluded.media_type END", tvLit)),
		// F-10 — tmdb_media_id follows the SAME winner as media_type so they never
		// desync: keep the stored 'tv' side's id, else take the incoming 'tv'
		// side's id, else (no 'tv' involved) take excluded's id.
		"tmdb_media_id": gorm.Expr(fmt.Sprintf(
			"CASE WHEN person_credits.media_type = %[1]s THEN person_credits.tmdb_media_id "+
				"WHEN excluded.media_type = %[1]s THEN excluded.tmdb_media_id "+
				"ELSE excluded.tmdb_media_id END", tvLit)),

		"title": gorm.Expr("excluded.title"),

		// F-04 — six TMDB-owned columns swap RAW↔COALESCE via enrich().
		"original_title": enrich("original_title"),
		"year":           enrich("year"),

		// character_name is written RAW in BOTH variants: the read paths
		// (ListBy*WithTextFallback) resolve requested-lang → en-US →
		// person_credits.character_name via the person_credits_texts side table,
		// so this base tier is effectively masked for localised reads. RAW here
		// is safe and preserves prior behaviour (AUDIT-S3: intentionally not
		// COALESCE-guarded).
		"character_name": gorm.Expr("excluded.character_name"),
		"kind":           gorm.Expr("excluded.kind"),
		"department":     gorm.Expr("excluded.department"),
		"job":            gorm.Expr("excluded.job"),

		"poster_path":  enrich("poster_path"),
		"vote_average": enrich("vote_average"),
		"tmdb_votes":   enrich("tmdb_votes"),
		// F-06 — episode_count joins the split: COALESCE on the series-worker seam
		// (0→NULL refresh must not null-clobber the aggregate total), RAW on the
		// person-worker seam (authoritative 0-episodes self-heal, symmetric to F-04).
		"episode_count": enrich("episode_count"),

		// credit_order — series-worker aggregate_credits is the ONLY source;
		// COALESCE-guarded in BOTH variants so a later order-less write never nulls
		// it (#1087b).
		"credit_order": gorm.Expr("COALESCE(excluded.credit_order, person_credits.credit_order)"),
		// last_appearance_season — portable MAX-merge in BOTH variants (#1090).
		// NULL-safe on both sides; never regresses a higher stored value.
		"last_appearance_season": gorm.Expr(
			"CASE " +
				"WHEN excluded.last_appearance_season IS NULL THEN person_credits.last_appearance_season " +
				"WHEN person_credits.last_appearance_season IS NULL THEN excluded.last_appearance_season " +
				"WHEN excluded.last_appearance_season > person_credits.last_appearance_season THEN excluded.last_appearance_season " +
				"ELSE person_credits.last_appearance_season END"),

		"updated_at": gorm.Expr("excluded.updated_at"),
	}
}
