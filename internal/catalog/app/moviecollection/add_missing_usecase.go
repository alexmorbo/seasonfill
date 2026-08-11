package moviecollection

import (
	"context"
	"fmt"
	"log/slog"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// AddMissingRequest asks the usecase to add every part of collection
// TMDBCollectionID that is NOT already in InstanceName's library. The
// quality/root/availability knobs are per-batch (applied to every add).
type AddMissingRequest struct {
	InstanceName        domain.InstanceName
	TMDBCollectionID    int
	QualityProfileID    int
	RootFolderPath      string
	Monitored           bool
	MinimumAvailability string // "" ⇒ "released" downstream
	SearchOnAdd         bool
}

// PartOutcome is the per-part result of an add-all-missing run.
type PartOutcome struct {
	TMDBID        int
	Title         string
	RadarrMovieID int
	AlreadyAdded  bool   // Radarr idempotent already-present
	Skipped       bool   // already in library (not attempted)
	Err           string // non-empty ⇒ this part's add failed (batch continued)
}

// AddMissingSummary aggregates one run. Requested = total parts in the
// collection; AlreadyPresent = skipped (in library); Added = newly added or
// Radarr-idempotent; Failed = per-part errors. The batch NEVER aborts on a
// single failure.
type AddMissingSummary struct {
	Requested      int
	Added          int
	AlreadyPresent int
	Failed         int
	Parts          []PartOutcome
}

// AddMissingUseCase adds a collection's missing parts to a Radarr instance,
// reusing the R-3 add-to-Radarr flow via the MovieAdder port. No REST route (R-6).
type AddMissingUseCase struct {
	reader ports.MovieCollectionsReader
	adder  MovieAdder
	log    *slog.Logger
}

// NewAddMissingUseCase panics on nil deps — init-time bug.
func NewAddMissingUseCase(reader ports.MovieCollectionsReader, adder MovieAdder, log *slog.Logger) *AddMissingUseCase {
	if reader == nil {
		panic("NewAddMissingUseCase: reader required")
	}
	if adder == nil {
		panic("NewAddMissingUseCase: adder required")
	}
	if log == nil {
		panic("NewAddMissingUseCase: log required")
	}
	return &AddMissingUseCase{reader: reader, adder: adder, log: log}
}

// AddAllMissing resolves the collection parts, skips the in-library ones and the
// tmdb-less ones (a Radarr orphan sharing a collection_id — cannot be added), and
// adds the rest. Per-part errors are collected; the batch continues. Returns an
// error ONLY when the parts read itself fails.
func (uc *AddMissingUseCase) AddAllMissing(ctx context.Context, req AddMissingRequest) (AddMissingSummary, error) {
	if req.TMDBCollectionID == 0 {
		return AddMissingSummary{}, fmt.Errorf("add all missing: tmdb_collection_id must be non-zero")
	}
	parts, err := uc.reader.ListPartsWithMembership(ctx, req.TMDBCollectionID, string(req.InstanceName))
	if err != nil {
		return AddMissingSummary{}, fmt.Errorf("add all missing: list parts: %w", err)
	}

	summary := AddMissingSummary{Requested: len(parts), Parts: make([]PartOutcome, 0, len(parts))}
	for _, p := range parts {
		out := PartOutcome{TMDBID: p.TMDBID, Title: p.Title}
		switch {
		case p.InLibrary:
			out.Skipped = true
			summary.AlreadyPresent++
		case p.TMDBID == 0:
			out.Skipped = true
			out.Err = "no tmdb id"
			summary.Failed++
		default:
			res, aerr := uc.adder.Add(ctx, AddMovieRequest{
				InstanceName:        req.InstanceName,
				TMDBID:              p.TMDBID,
				QualityProfileID:    req.QualityProfileID,
				RootFolderPath:      req.RootFolderPath,
				Monitored:           req.Monitored,
				MinimumAvailability: req.MinimumAvailability,
				SearchOnAdd:         req.SearchOnAdd,
			})
			if aerr != nil {
				out.Err = aerr.Error()
				summary.Failed++
				uc.log.WarnContext(ctx, "moviecollection.add_missing.part_failed",
					slog.Int("tmdb_id", p.TMDBID),
					slog.String("instance", string(req.InstanceName)),
					slog.String("error", aerr.Error()))
			} else {
				out.RadarrMovieID = res.RadarrMovieID
				out.AlreadyAdded = res.AlreadyAdded
				summary.Added++
			}
		}
		summary.Parts = append(summary.Parts, out)
	}

	uc.log.InfoContext(ctx, "moviecollection.add_missing.done",
		slog.Int("collection_id", req.TMDBCollectionID),
		slog.String("instance", string(req.InstanceName)),
		slog.Int("requested", summary.Requested),
		slog.Int("added", summary.Added),
		slog.Int("already_present", summary.AlreadyPresent),
		slog.Int("failed", summary.Failed),
	)
	return summary, nil
}
