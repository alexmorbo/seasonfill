package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	enrichmentpkg "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// PeopleRepository persists the canonical `people` table (PRD §5.3).
// Upsert is idempotent and hydration-preserving: re-upserting a stub
// payload over an existing 'full' row keeps 'full' (defensive
// against series_enrichment_worker clobbering a
// person_enrichment_worker hydration). Get composes the row with
// its resolved biography via JOIN against person_biographies using
// the shared §5.6 helper.
type PeopleRepository struct {
	db *gorm.DB
}

func NewPeopleRepository(db *gorm.DB) *PeopleRepository {
	return &PeopleRepository{db: db}
}

// Get fetches by primary key and resolves the biography in the
// requested language via the shared §5.6 fallback helper. Empty
// language is normalised to en-US by the helper. Returns
// ports.ErrNotFound on miss of the person row; a person without any
// biography row returns the Person with empty Biography /
// BiographyLanguage (NOT an error — stub persons frequently have no
// biography yet).
func (r *PeopleRepository) Get(ctx context.Context, id int64, language string) (people.Person, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	var m database.PeopleModel
	err := db.Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return people.Person{}, ports.ErrNotFound
		}
		return people.Person{}, fmt.Errorf("get person: %w", err)
	}
	person := toPerson(m)

	// Resolve biography via the shared helper. A missing row is not
	// an error here — stub persons frequently land without any bio.
	var bio database.PersonBiographyModel
	if err := pickLanguageFallback(
		ctx, r.db,
		"person_biographies", "person_id",
		id, language,
		&bio,
	); err != nil {
		return people.Person{}, fmt.Errorf("resolve biography: %w", err)
	}
	if bio.PersonID != 0 && bio.Biography != nil {
		person.Biography = *bio.Biography
		person.BiographyLanguage = bio.Language
	}
	return person, nil
}

// GetByTMDBID looks up the canon row by TMDB id. The partial unique
// index guarantees at most one row. Biography is NOT resolved here
// (caller passes id to Get for that) — this is the hot path used by
// series_enrichment_worker to resolve "do I already have this
// person?" without touching person_biographies.
func (r *PeopleRepository) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (people.Person, error) {
	var m database.PeopleModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id = ?", tmdbID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return people.Person{}, ports.ErrNotFound
		}
		return people.Person{}, fmt.Errorf("get person by tmdb_id: %w", err)
	}
	return toPerson(m), nil
}

// ListByIDs returns rows for the given ids in id-ascending order;
// missing ids are silently skipped (callers that need a presence
// check go through Get / GetByTMDBID). Biography is NOT resolved —
// list paths render compact rows.
func (r *PeopleRepository) ListByIDs(ctx context.Context, ids []int64) ([]people.Person, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var models []database.PeopleModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id IN ?", ids).
		Order("id ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	out := make([]people.Person, 0, len(models))
	for _, m := range models {
		out = append(out, toPerson(m))
	}
	return out, nil
}

// Upsert inserts or updates the canon row. Conflict target is the
// natural key (tmdb_id) when the caller supplies one, otherwise PK
// (id). Idempotency: a no-op upsert leaves every column byte-equal
// except updated_at.
//
// Hydration handling — three rules:
//  1. Empty Hydration on input is normalised to HydrationStub.
//  2. An explicit HydrationFull insert / upsert is always honoured.
//  3. A HydrationStub upsert over an existing HydrationFull row
//     PRESERVES 'full' (defensive — protects against
//     series_enrichment_worker accidentally downgrading a row that
//     person_enrichment_worker already lifted).
//
// Rule (3) keeps the row's hydration monotonic on the
// stub → full axis; non-hydration fields still merge in.
func (r *PeopleRepository) Upsert(ctx context.Context, p people.Person) (int64, error) {
	if p.Name == "" {
		return 0, fmt.Errorf("upsert person: name must be non-empty")
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.Hydration == "" {
		p.Hydration = people.HydrationStub
	}
	if !p.Hydration.IsValid() {
		return 0, fmt.Errorf("upsert person: invalid hydration %q", p.Hydration)
	}

	db := dbFromContext(ctx, r.db).WithContext(ctx)

	// Rule (3): preserve full hydration on a stub upsert over an
	// existing full row.
	if p.Hydration == people.HydrationStub && p.TMDBID != nil {
		var existing database.PeopleModel
		err := db.Select("id, hydration").
			Where("tmdb_id = ?", *p.TMDBID).
			First(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("upsert person: probe existing: %w", err)
		}
		if err == nil && existing.Hydration == string(people.HydrationFull) {
			p.Hydration = people.HydrationFull
		}
	}

	m := fromPerson(p)
	// No PK + no natural key ⇒ pure INSERT, no ON CONFLICT clause.
	// Previously this branch emitted `clause.OnConflict{DoNothing:
	// false}` which serialized to a bare `ON CONFLICT DO UPDATE`;
	// SQLite tolerates the empty target, Postgres rejects it with
	// SQLSTATE 42601 ("requires inference specification or constraint
	// name"). Story 424a dual-backend migration caught this on the
	// IMDB-only orphan path (tmdb_id NULL with non-nil imdb_id).
	switch {
	case m.ID != 0:
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns(peopleUpdateCols()),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert person: %w", err)
		}
	case m.TMDBID != nil:
		// Partial unique index on tmdb_id WHERE tmdb_id IS NOT NULL —
		// SQLite + Postgres both require the index predicate to be
		// repeated in the ON CONFLICT target so the planner picks the
		// partial index rather than rejecting "no matching constraint".
		conflict := clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
			DoUpdates:   clause.AssignmentColumns(peopleUpdateCols()),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert person: %w", err)
		}
	default:
		// No PK and no natural key — pure insert. GORM assigns id.
		if err := db.Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert person: %w", err)
		}
	}
	return m.ID, nil
}

// peopleUpdateCols lists the columns updated on a conflict. id /
// created_at are excluded so the row's identity and insertion
// timestamp survive the upsert path.
//
// NOTE — enrichment_synced_at is intentionally NOT in this list. The
// general Upsert path must NOT touch the per-person enrichment
// freshness column: only PersonWorker.MarkSynced is allowed to bump
// it (the post-tx success stamp). A stub-upsert from series_worker's
// credit fan-out, or an IMDB-only orphan write, would otherwise blank
// the column and trigger an unbounded re-enrichment loop on the next
// dispatcher tick. Matches the COALESCE protection on the series
// canon's enrichment_*_synced_at columns (series_repository.go
// seriesUpsertAssignments).
func peopleUpdateCols() []string {
	return []string{
		"tmdb_id", "imdb_id",
		"hydration", "name", "original_name",
		"gender", "birthday", "deathday",
		"place_of_birth", "known_for_department",
		"popularity", "profile_asset",
		"updated_at",
	}
}

// MarkSynced stamps people.enrichment_synced_at = now for one person row.
// Single-column UPDATE — no other columns are touched, so a concurrent
// upsert that COALESCEs enrichment_synced_at preserves the value we just
// wrote (and vice versa). PersonWorker calls this after a successful
// hydration tx; ClearOnSuccess on enrichment_errors fires in parallel.
//
// Idempotent on the column: re-bumping a fresh row is a no-op semantically
// (just refreshes the timestamp). The caller passes the desired wall
// time so tests + production share the same clock seam.
func (r *PeopleRepository) MarkSynced(ctx context.Context, personID int64, now time.Time) error {
	if personID == 0 {
		return fmt.Errorf("mark person synced: person_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("people").
		Where("id = ?", personID).
		Updates(map[string]any{
			"enrichment_synced_at": now.UTC(),
			"updated_at":           now.UTC(),
		}).Error
	if err != nil {
		return fmt.Errorf("mark person synced: %w", err)
	}
	return nil
}

// ListStaleForTMDB returns person ids whose enrichment_synced_at is
// NULL or older than now-ttl, capped at `limit` rows ordered by id ASC.
// Excludes persons with > 5 enrichment_errors attempts (terminal
// retry-give-up, matches series-side ListLibraryWithIMDBStale shape).
//
// Used by the D-3 dispatcher loop in BuildEnrichment to enqueue
// person refresh jobs. Selectivity is good on a typical library
// (~3000 person rows; most are fresh within 30d so the WHERE filter
// narrows to a few hundred stale rows at most).
func (r *PeopleRepository) ListStaleForTMDB(ctx context.Context, ttl time.Duration, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 200
	}
	cutoff := time.Now().UTC().Add(-ttl)
	var ids []int64
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("people AS p").
		Select("p.id").
		Where("p.tmdb_id IS NOT NULL").
		Where("(p.enrichment_synced_at IS NULL OR p.enrichment_synced_at < ?)", cutoff).
		Where(`NOT EXISTS (
		    SELECT 1 FROM enrichment_errors ee
		     WHERE ee.entity_type = 'person'
		       AND ee.entity_id = p.id
		       AND ee.source = ?
		       AND ee.attempts > 5)`, string(enrichmentpkg.SourceTMDBPerson)).
		Order("p.id ASC").
		Limit(limit).
		Pluck("p.id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("list people stale for tmdb: %w", err)
	}
	return ids, nil
}

func toPerson(m database.PeopleModel) people.Person {
	return people.Person{
		ID:                 m.ID,
		TMDBID:             m.TMDBID,
		IMDBID:             m.IMDBID,
		Hydration:          people.Hydration(m.Hydration),
		Name:               m.Name,
		OriginalName:       m.OriginalName,
		Gender:             m.Gender,
		Birthday:           m.Birthday,
		Deathday:           m.Deathday,
		PlaceOfBirth:       m.PlaceOfBirth,
		KnownForDepartment: m.KnownForDepartment,
		Popularity:         m.Popularity,
		ProfileAsset:       m.ProfileAsset,
		EnrichmentSyncedAt: m.EnrichmentSyncedAt,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}

func fromPerson(p people.Person) database.PeopleModel {
	return database.PeopleModel{
		ID:                 p.ID,
		TMDBID:             p.TMDBID,
		IMDBID:             p.IMDBID,
		Hydration:          string(p.Hydration),
		Name:               p.Name,
		OriginalName:       p.OriginalName,
		Gender:             p.Gender,
		Birthday:           p.Birthday,
		Deathday:           p.Deathday,
		PlaceOfBirth:       p.PlaceOfBirth,
		KnownForDepartment: p.KnownForDepartment,
		Popularity:         p.Popularity,
		ProfileAsset:       p.ProfileAsset,
		EnrichmentSyncedAt: p.EnrichmentSyncedAt,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}
