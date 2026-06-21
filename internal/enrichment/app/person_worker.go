// Package enrichment — Story 212 person worker.
//
// PersonWorker is the EntityKindPerson handler. Workflow per PRD §5.5:
//   1. Read person row.
//   2. Skip if hydration=full AND person.EnrichmentSyncedAt is within TTL.
//   3. tmdbClient.GetPerson → MapPersonToDomain → person + credits.
//   4. ONE tx: upsert people (full) + person_biographies (en-US) +
//      person_credits (batch) + external_ids (imdb / homepage / socials).
//   5. Stamp people.enrichment_synced_at via MarkSynced + clear any
//      outstanding enrichment_errors row.
//
// Shared *tmdb.Client invariant: this file does NOT import the TMDB
// client constructor; the wiring layer threads the same instance held
// by series_worker (preserves the 5-rps token bucket).

package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// PersonWorkerDeps mirrors SeriesWorkerDeps verbatim in style — each
// repository is a named field. Logger / Clock defaults match 211.
type PersonWorkerDeps struct {
	TMDB              TMDBClient
	Tx                Transactor
	Language          string // "en-US"; ru lands in C-5
	People            PeopleWritePort
	PersonBiographies PersonBiographiesPort
	PersonCredits     PersonCreditsPort
	ExternalIDs       ExternalIDsRepoPort
	EnrichmentErrors  EnrichmentErrorRepo
	Logger            *slog.Logger
	Clock             func() time.Time
}

// PersonWorker is the bound worker. Construct via NewPersonWorker.
type PersonWorker struct {
	deps PersonWorkerDeps
}

// personCreditsBatchSize bounds INSERT-row count per chunk. PRD §5.10
// risk note: ~3000 people × ~100 credits = ~300k rows over the whole
// library; chunking keeps a single statement bounded under sqlite's
// SQLITE_MAX_VARIABLE_NUMBER (32766) — 500 rows × ~12 cols = ~6000
// vars, comfortably under the limit on both dialects.
const personCreditsBatchSize = 500

// NewPersonWorker validates every required dependency. Mirrors
// NewSeriesWorker's error shape.
func NewPersonWorker(deps PersonWorkerDeps) (*PersonWorker, error) {
	if deps.TMDB == nil {
		return nil, errors.New("enrichment.person_worker: TMDB client required")
	}
	if deps.Tx == nil {
		return nil, errors.New("enrichment.person_worker: Transactor required")
	}
	if deps.People == nil || deps.PersonBiographies == nil ||
		deps.PersonCredits == nil || deps.ExternalIDs == nil ||
		deps.EnrichmentErrors == nil {
		return nil, errors.New("enrichment.person_worker: every repository port is required")
	}
	if deps.Language == "" {
		deps.Language = "en-US"
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &PersonWorker{deps: deps}, nil
}

// Handle is the dispatcher-facing entry point. personID is a CANON
// people.id. Returns nil on every terminal outcome (ok / not_found /
// retryable error journalled).
func (w *PersonWorker) Handle(ctx context.Context, personID int64) error {
	start := w.deps.Clock()
	log := w.deps.Logger.With(
		slog.String("entity_type", string(enrichment.EntityTypePerson)),
		slog.Int64("entity_id", personID),
		slog.String("source", string(enrichment.SourceTMDBPerson)),
	)

	// 1. Load person — we need tmdb_id + current hydration.
	person, err := w.deps.People.Get(ctx, personID, w.deps.Language)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			log.WarnContext(ctx, "enrichment.person.handle.person_missing")
			return nil
		}
		return fmt.Errorf("person worker: load person: %w", err)
	}
	if person.TMDBID == nil {
		w.journalNotFound(ctx, personID, "no tmdb_id on person", start)
		return nil
	}
	tmdbPersonID := int64(*person.TMDBID)

	// 2. Staleness short-circuit — read person.EnrichmentSyncedAt
	//    directly. Requires hydration=full (a stub row is always
	//    "stale" from the worker's POV — the dispatcher enqueues
	//    stubs explicitly, but a fresh-skip on stub would be wrong).
	if person.Hydration == people.HydrationFull && person.EnrichmentSyncedAt != nil {
		ttl := enrichment.TTL(enrichment.SourceTMDBPerson, enrichment.KindPerson)
		if ttl > 0 && w.deps.Clock().Sub(*person.EnrichmentSyncedAt) < ttl {
			log.DebugContext(ctx, "enrichment.person.handle.fresh_skip",
				slog.String("domain", "enrichment"),
				slog.Time("synced_at", *person.EnrichmentSyncedAt),
			)
			return nil
		}
	}

	// Load any current error row (for attempts counter on retry path).
	prevAttempts := 0
	if errRow, errErr := w.deps.EnrichmentErrors.GetByEntitySource(ctx,
		enrichment.EntityTypePerson, personID, enrichment.SourceTMDBPerson); errErr == nil {
		prevAttempts = errRow.Attempts
	} else if !errors.Is(errErr, ports.ErrNotFound) {
		log.WarnContext(ctx, "enrichment.person.handle.error_row_read_failed",
			slog.String("error", errErr.Error()))
	}

	// 3. Fetch /person/{id} (single round-trip, append_to_response).
	resp, err := w.deps.TMDB.GetPerson(ctx, tmdbPersonID, w.deps.Language)
	if err != nil {
		return w.handleTMDBError(ctx, personID, "GetPerson", err, prevAttempts, start)
	}

	// 4. Map outside the tx — pure CPU, no I/O.
	mapped, credits := tmdb.MapPersonToDomain(resp)
	mapped.ID = personID // preserve PK across the upsert path
	xids := personExternalIDsFromResponse(resp)
	bio := personBiographyRow(personID, w.deps.Language, mapped.Biography)

	// 5. ONE tx: people → person_biographies → person_credits → external_ids.
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		return w.applyAll(txCtx, personID, mapped, bio, credits, xids)
	})
	if err != nil {
		return w.handleTMDBError(ctx, personID, "tx", err, prevAttempts, start)
	}

	// 6. Journal success — stamp canon column + clear any pending error row.
	now := w.deps.Clock()
	dur := int(now.Sub(start).Milliseconds())
	w.journalOK(ctx, personID, now)

	log.InfoContext(ctx, "enrichment.person.handle.ok",
		slog.String("domain", "enrichment"),
		slog.Int64("tmdb_person_id", tmdbPersonID),
		slog.Int("tmdb_credit_count", len(credits)),
		slog.Int("duration_ms", dur),
	)
	return nil
}

// applyAll runs every repo write inside ONE tx. Order: people →
// biographies → credits (batched) → external_ids. The people upsert
// is BY PK (mapped.ID = personID) so we lift the existing stub row
// rather than insert a new one — preserving series_people foreign
// keys.
func (w *PersonWorker) applyAll(
	txCtx context.Context,
	personID int64,
	person people.Person,
	bio people.PersonBiography,
	credits []people.PersonCredit,
	xids []externalIDRow,
) error {
	// 1. people — full hydration upsert (PK target).
	if _, err := w.deps.People.Upsert(txCtx, person); err != nil {
		return fmt.Errorf("upsert person: %w", err)
	}

	// 2. person_biographies (en-US) — only write when biography text
	//    is non-empty. Empty bio is normal for a Person with no TMDB
	//    biography text yet; writing an empty row pollutes the
	//    §5.6 fallback (it would return "" as a successful match).
	if bio.Biography != nil && *bio.Biography != "" {
		if err := w.deps.PersonBiographies.Upsert(txCtx, bio); err != nil {
			return fmt.Errorf("upsert person_biographies: %w", err)
		}
	}

	// 3. person_credits — wire PersonID, chunk into batches of 500.
	for i := range credits {
		credits[i].PersonID = personID
	}
	if err := w.batchCredits(txCtx, credits); err != nil {
		return err
	}

	// 4. external_ids: imdb + homepage + socials + tvdb.
	for _, x := range xids {
		if x.value == "" {
			continue
		}
		if err := w.deps.ExternalIDs.Upsert(
			txCtx, enrichment.EntityTypePerson, personID, x.provider, x.value,
		); err != nil {
			return fmt.Errorf("upsert external_id: %w", err)
		}
	}
	return nil
}

// batchCredits chunks `credits` into runs of personCreditsBatchSize
// and writes via PersonCreditsRepository.BatchUpsert. Empty slice is a
// no-op (mapper-empty payloads — eg an actor with no TMDB filmography
// — must not error).
func (w *PersonWorker) batchCredits(txCtx context.Context, credits []people.PersonCredit) error {
	if len(credits) == 0 {
		return nil
	}
	for start := 0; start < len(credits); start += personCreditsBatchSize {
		end := min(start+personCreditsBatchSize, len(credits))
		if _, err := w.deps.PersonCredits.BatchUpsert(txCtx, credits[start:end]); err != nil {
			return fmt.Errorf("batch upsert person_credits [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

// ---- mapping helpers (private) -------------------------------------

// externalIDRow is the (provider, value) tuple the worker passes to
// the external_ids repo. Order is deterministic so re-running yields
// byte-equal table state.
type externalIDRow struct {
	provider string
	value    string
}

// personExternalIDsFromResponse flattens the TMDB person external_ids
// embed + homepage into one slice. NormaliseIMDBID is applied via the
// mapper layer's contract (the imdb_id on the response is already
// normalised by MapPersonToDomain → person.IMDBID, but the external_ids
// table stores the raw provider value, so we re-normalise here).
func personExternalIDsFromResponse(p *tmdb.PersonResponse) []externalIDRow {
	if p == nil {
		return nil
	}
	out := make([]externalIDRow, 0, 7)
	if id := tmdb.NormaliseIMDBID(p.IMDBID); id != "" {
		out = append(out, externalIDRow{provider: "imdb", value: id})
	}
	if p.Homepage != "" {
		out = append(out, externalIDRow{provider: "homepage", value: p.Homepage})
	}
	if p.ExternalIDs != nil {
		x := p.ExternalIDs
		if id := tmdb.NormaliseIMDBID(x.IMDBID); id != "" {
			// De-dup against the top-level imdb_id leg.
			if len(out) == 0 || out[0].value != id {
				out = append(out, externalIDRow{provider: "imdb", value: id})
			}
		}
		if x.WikidataID != "" {
			out = append(out, externalIDRow{provider: "wikidata", value: x.WikidataID})
		}
		if x.FacebookID != "" {
			out = append(out, externalIDRow{provider: "facebook", value: x.FacebookID})
		}
		if x.InstagramID != "" {
			out = append(out, externalIDRow{provider: "instagram", value: x.InstagramID})
		}
		if x.TwitterID != "" {
			out = append(out, externalIDRow{provider: "twitter", value: x.TwitterID})
		}
		if x.TVDBID != nil {
			out = append(out, externalIDRow{provider: "tvdb", value: itoa(int64(*x.TVDBID))})
		}
	}
	return out
}

// personBiographyRow builds the (person_id, language) biography row.
// Empty biography text yields a row whose Biography pointer is nil —
// applyAll filters it out so the (entity, lang) PK isn't claimed by
// an empty record (PRD §5.6 fallback contract).
func personBiographyRow(personID int64, language, text string) people.PersonBiography {
	row := people.PersonBiography{PersonID: personID, Language: language}
	if text != "" {
		t := text
		row.Biography = &t
	}
	return row
}

// ---- error handling + journal helpers ------------------------------

// handleTMDBError records an enrichment_errors row for a TMDB person
// fetch failure. TMDB 404 → terminalAttempts (no retry); other errors
// → previousAttempts+1 with NextAttemptAt backoff.
func (w *PersonWorker) handleTMDBError(ctx context.Context, personID int64, op string, err error, previousAttempts int, start time.Time) error {
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	log := w.deps.Logger.With(
		slog.String("domain", "enrichment"),
		slog.String("entity_type", string(enrichment.EntityTypePerson)),
		slog.Int64("entity_id", personID),
		slog.String("source", string(enrichment.SourceTMDBPerson)),
		slog.String("op", op),
	)

	var apiErr *tmdb.APIError
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		w.recordPersonError(ctx, personID, err, terminalAttempts, nil, log)
		log.InfoContext(ctx, "enrichment.person.handle.not_found",
			slog.Int("duration_ms", durMs),
		)
		return nil
	}

	attempts := previousAttempts + 1
	next := enrichment.NextAttemptAt(attempts, now)
	w.recordPersonError(ctx, personID, err, attempts, &next, log)
	log.WarnContext(ctx, "enrichment.person.handle.failed",
		slog.Int("attempts", attempts),
		slog.Time("next_attempt_at", next),
		slog.Int("duration_ms", durMs),
		slog.String("error", err.Error()),
	)
	return nil
}

// recordPersonError writes the (person, tmdb_person) enrichment_errors row.
func (w *PersonWorker) recordPersonError(
	ctx context.Context,
	personID int64,
	cause error,
	attempts int,
	nextAttemptAt *time.Time,
	log *slog.Logger,
) {
	now := w.deps.Clock()
	rec := enrichment.EnrichmentError{
		EntityType:    enrichment.EntityTypePerson,
		EntityID:      personID,
		Source:        enrichment.SourceTMDBPerson,
		LastError:     cause.Error(),
		Attempts:      attempts,
		LastSeenAt:    now,
		NextAttemptAt: nextAttemptAt,
	}
	if err := w.deps.EnrichmentErrors.RecordFailure(ctx, rec); err != nil {
		log.WarnContext(ctx, "enrichment.person.handle.record_failure_failed",
			slog.String("error", err.Error()))
	}
}

// journalOK stamps people.enrichment_synced_at = now and clears any
// outstanding enrichment_errors row for (person, tmdb_person).
func (w *PersonWorker) journalOK(ctx context.Context, personID int64, now time.Time) {
	if err := w.deps.People.MarkSynced(ctx, personID, now); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.person.handle.mark_synced_failed",
			slog.String("domain", "enrichment"),
			slog.Int64("entity_id", personID),
			slog.String("error", err.Error()))
	}
	if err := w.deps.EnrichmentErrors.ClearOnSuccess(ctx,
		enrichment.EntityTypePerson, personID, enrichment.SourceTMDBPerson); err != nil {
		w.deps.Logger.WarnContext(ctx, "enrichment.person.handle.clear_error_failed",
			slog.String("domain", "enrichment"),
			slog.Int64("entity_id", personID),
			slog.String("error", err.Error()))
	}
}

func (w *PersonWorker) journalNotFound(ctx context.Context, personID int64, msg string, start time.Time) {
	now := w.deps.Clock()
	durMs := int(now.Sub(start).Milliseconds())
	log := w.deps.Logger.With(
		slog.String("domain", "enrichment"),
		slog.String("entity_type", string(enrichment.EntityTypePerson)),
		slog.Int64("entity_id", personID),
		slog.String("source", string(enrichment.SourceTMDBPerson)),
	)
	w.recordPersonError(ctx, personID, errors.New(msg), terminalAttempts, nil, log)
	log.InfoContext(ctx, "enrichment.person.handle.not_found",
		slog.String("reason", msg),
		slog.Int("duration_ms", durMs),
	)
}
