package wiring

import (
	"log/slog"
	"time"

	"gorm.io/gorm"

	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdadapters "github.com/alexmorbo/seasonfill/internal/mediadetail/adapters"
	mdapp "github.com/alexmorbo/seasonfill/internal/mediadetail/app"
	mddomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MediaDetailBundle groups the universal MediaDetail engine (ADR-0022) plus the
// S2a late-bind holders. The section registry now carries the (movie,text) and
// (series,text) plugins; the movie one is the LIVE driver (via MovieEngineFreshener,
// wired into the movie usecase's freshenerPort in BuildMovieDetail), the series one
// is registered but dormant (no runtime code drives the engine with a series MediaID).
type MediaDetailBundle struct {
	Registry  *mdapp.SectionRegistry
	Freshener *mdapp.Freshener
	Composer  *mdapp.Composer

	// MovieForce — late-bound movie worker for movieTextPlugin.Refresh. server.go
	// calls Set(movieEnrich.Worker) at the movie-worker late-bind zone (:990 area).
	MovieForce *mdadapters.MovieForceRefresherHolder
	// SeriesForce — late-bound SeriesWorker for seriesTextPlugin.Refresh. server.go
	// calls Set(enrichBundle.SeriesWorker) at the series late-bind zone (:566 area).
	SeriesForce *mdadapters.SeriesAllLangsRefresherHolder
	// SeriesOverview — dormant/unbound series overview probe (real Probe unreachable).
	SeriesOverview *mdadapters.SeriesOverviewStalenessHolder
}

// BuildMediaDetail assembles the engine and registers the S2a text plugins. db
// backs the movie canon + i18n-gap readers. domainLog is tagged "http". The
// movie-text REPLACE seam (MovieEngineFreshener) is created in BuildMovieDetail
// (Option R) and .Set(Freshener) in server.go once this engine exists.
func BuildMediaDetail(db *gorm.DB, log *slog.Logger) *MediaDetailBundle {
	domainLog := sharedports.DomainLogger(log, "http")
	registry := mdapp.NewSectionRegistry()
	freshener := mdapp.NewFreshener(registry, 5*time.Second, time.Now, domainLog)
	composer := mdapp.NewComposer(domainLog)

	movieForce := mdadapters.NewMovieForceRefresherHolder()
	seriesForce := mdadapters.NewSeriesAllLangsRefresherHolder()
	seriesOverview := mdadapters.NewSeriesOverviewStalenessHolder()

	moviePlugin := mdadapters.NewMovieTextPlugin(
		enrichpersistence.NewMovieRepository(db),
		enrichpersistence.NewMovieI18nReadRepository(db),
		movieForce,
		locale.Default(),
	)
	seriesPlugin := mdadapters.NewSeriesTextPlugin(seriesOverview, seriesForce)

	registry.Register(mddomain.MediaTypeMovie, moviePlugin)
	registry.Register(mddomain.MediaTypeSeries, seriesPlugin)

	return &MediaDetailBundle{
		Registry:       registry,
		Freshener:      freshener,
		Composer:       composer,
		MovieForce:     movieForce,
		SeriesForce:    seriesForce,
		SeriesOverview: seriesOverview,
	}
}
