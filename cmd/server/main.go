package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	appenrich "github.com/alexmorbo/seasonfill/application/enrichment"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/application/seriesrefresh"
	dompeople "github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		if err := runResetPassword(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "reset-password: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "auth-mode" {
		if err := runAuthMode(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "auth-mode: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "grabs" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: seasonfill grabs <reparse> [flags]")
			os.Exit(2)
		}
		switch os.Args[2] {
		case "reparse":
			if err := runReparseCLI(context.Background(), os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "reparse: %v\n", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown grabs subcommand: %s\n", os.Args[2])
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	_, err := runWithContext(ctx, nil)
	return err
}

// runWithContext is a thin shim preserved for the integration-test entry
// (main_test_entry.go → runForTest). Production run() and tests both go
// through this; the bus is exposed for E2E assertions.
func runWithContext(ctx context.Context, onReady func(*runtime.Bus)) (*runtime.Bus, error) {
	srv, err := New(ctx, Options{OnReady: onReady})
	if err != nil {
		return nil, err
	}
	if err := srv.Run(ctx); err != nil {
		return srv.bus, err
	}
	return srv.bus, nil
}

// runCooldownSweep is preserved for callers (and tests) that drive the
// sweep with a fixed cadence. New call sites should construct a
// sweepLoop directly so the cadence can be updated by the reload bus.
func runCooldownSweep(ctx context.Context, repo ports.CooldownRepository, every time.Duration, log *slog.Logger) {
	newSweepLoop(repo, every, log).Run(ctx)
}

// regrabInstanceRegistry adapts handlers.InstanceRegistry to the
// application/regrab.InstanceRegistry interface. The Get(name) →
// (scan.Instance, bool) semantics are a thin nil-safe wrapper.
type regrabInstanceRegistry struct {
	reg handlers.InstanceRegistry
}

func (r regrabInstanceRegistry) Get(name string) (scan.Instance, bool) {
	if r.reg.Load == nil {
		return scan.Instance{}, false
	}
	inst, ok := r.reg.Load()[name]
	return inst, ok
}

// qbitSettingsLoaderFunc is a function-typed shim that satisfies
// qbitSettingsLoader. Defined here so the fanout closure can be
// declared inline above without a named struct.
type qbitSettingsLoaderFunc func(ctx context.Context) map[string]regrab.Settings

func (f qbitSettingsLoaderFunc) Load(ctx context.Context) map[string]regrab.Settings {
	return f(ctx)
}

// personCreditsAdapter projects repositories.PersonCredit rows
// down to the H-1 composer-internal PersonCreditRef shape (Story
// 216). The projection is cheap (two field copies) and keeps the
// application layer free of the repository's wide PersonCredit
// struct.
type personCreditsAdapter struct {
	r *repositories.PersonCreditsRepository
}

func (a personCreditsAdapter) ListByPerson(ctx context.Context, personID int64) ([]seriesdetail.PersonCreditRef, error) {
	rows, err := a.r.ListByPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	out := make([]seriesdetail.PersonCreditRef, 0, len(rows))
	for _, pc := range rows {
		out = append(out, seriesdetail.PersonCreditRef{
			MediaType:   pc.MediaType,
			TMDBMediaID: pc.TMDBMediaID,
		})
	}
	return out, nil
}

// peopleReaderAdapter projects PeopleRepository onto the H-2
// PeopleReader port — GetByTMDBID for the hot resolution path,
// GetWithBio (renamed from repo's Get) for the bio-resolving
// path. The renaming is local; the production repository's
// method is `Get(ctx, id, language)`.
type peopleReaderAdapter struct {
	r *repositories.PeopleRepository
}

func (a peopleReaderAdapter) GetByTMDBID(ctx context.Context, tmdbID int) (dompeople.Person, error) {
	return a.r.GetByTMDBID(ctx, tmdbID)
}

func (a peopleReaderAdapter) GetWithBio(ctx context.Context, id int64, language string) (dompeople.Person, error) {
	return a.r.Get(ctx, id, language)
}

// personCreditsReaderAdapter projects PersonCreditsRepository
// onto the H-2 PersonCreditsReader port. The repository's
// ListByPerson returns []PersonCreditModel; the adapter converts
// to []dompeople.PersonCredit row by row.
type personCreditsReaderAdapter struct {
	r *repositories.PersonCreditsRepository
}

func (a personCreditsReaderAdapter) ListByPerson(ctx context.Context, personID int64) ([]dompeople.PersonCredit, error) {
	rows, err := a.r.ListByPerson(ctx, personID)
	if err != nil {
		return nil, err
	}
	out := make([]dompeople.PersonCredit, 0, len(rows))
	for _, m := range rows {
		out = append(out, modelToPersonCredit(m))
	}
	return out, nil
}

// modelToPersonCredit maps PersonCreditModel → domain
// PersonCredit. Year passes through as the synthetic date
// (year, 1, 1) so downstream code that reads Year from
// ReleaseDate works; PosterPath is mapped to PosterAsset (the
// v1 H-2 layer treats both as pass-through strings, formal asset
// migration deferred).
func modelToPersonCredit(m database.PersonCreditModel) dompeople.PersonCredit {
	var rel *time.Time
	if m.Year != nil {
		t := time.Date(*m.Year, 1, 1, 0, 0, 0, 0, time.UTC)
		rel = &t
	}
	return dompeople.PersonCredit{
		ID:            m.ID,
		PersonID:      m.PersonID,
		MediaType:     m.MediaType,
		TMDBMediaID:   int64(m.TMDBMediaID),
		TMDBCreditID:  m.TMDBCreditID,
		Kind:          dompeople.SeriesCreditKind(m.Kind),
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		CharacterName: m.CharacterName,
		Department:    m.Department,
		Job:           m.Job,
		EpisodeCount:  m.EpisodeCount,
		ReleaseDate:   rel,
		PosterAsset:   m.PosterPath,
		TMDBRating:    m.VoteAverage,
		TMDBVotes:     m.TMDBVotes,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

// personEnqueuerHolder late-binds the enrichment dispatcher into
// the H-2 people use case. The dispatcher is constructed inside
// wireEnrichment, but the people use case is built earlier (so it
// can be passed to httpserver.NewServer). The holder is wired
// after enrichBundle is assembled; until then Enqueue no-ops
// (nil-OK by contract — the use case still returns 200 + degraded
// for stub persons on cold boot / disabled enrichment).
type personEnqueuerHolder struct {
	inner appenrich.Dispatcher
}

func (h *personEnqueuerHolder) set(d appenrich.Dispatcher) { h.inner = d }

func (h *personEnqueuerHolder) Enqueue(kind appenrich.EntityKind, id int64, p appenrich.Priority) {
	if h.inner == nil {
		return
	}
	h.inner.Enqueue(kind, id, p)
}

// Close satisfies appenrich.Dispatcher so the same holder serves
// both PersonEnqueuer (Enqueue-only) and seriesrefresh.Deps.Dispatcher
// (Enqueue + Close). The dispatcher's actual Close runs via
// enrichBundle.Dispatcher.Close() at shutdown, so this holder no-ops.
func (h *personEnqueuerHolder) Close() {}

// seriesRefreshSeriesAdapter projects SeriesRepository.Get onto the
// thin seriesrefresh.CanonView shape so the use case stays free of
// the domain/series import. Story 218 (E-2).
type seriesRefreshSeriesAdapter struct {
	r *repositories.SeriesRepository
}

func (a seriesRefreshSeriesAdapter) Get(ctx context.Context, id int64) (seriesrefresh.CanonView, error) {
	c, err := a.r.Get(ctx, id)
	if err != nil {
		return seriesrefresh.CanonView{}, err
	}
	return seriesrefresh.CanonView{ID: c.ID, IMDBID: c.IMDBID}, nil
}

// seriesRefreshCastAdapter implements seriesrefresh.TopCastReader by
// calling SeriesPeopleRepository.ListBySeries (the composer's existing
// path) and slicing the first N person ids. Story 218 (E-2).
type seriesRefreshCastAdapter struct {
	r *repositories.SeriesPeopleRepository
}

func (a seriesRefreshCastAdapter) TopCastPersonIDs(ctx context.Context, seriesID int64, limit int) ([]int64, error) {
	credits, err := a.r.ListBySeries(ctx, seriesID, dompeople.SeriesCreditCast)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(credits) {
		limit = len(credits)
	}
	out := make([]int64, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, credits[i].PersonID)
	}
	return out, nil
}
