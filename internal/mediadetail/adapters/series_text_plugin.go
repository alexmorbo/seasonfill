package adapters

import (
	"context"
	"time"

	mdengapp "github.com/alexmorbo/seasonfill/internal/mediadetail/app"
	mdengdomain "github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// mdengSectionPlugin aliases the engine SectionPlugin port so the plugin
// constructors in this package advertise it without leaking the engine import
// into every call site. Declared once here.
type mdengSectionPlugin = mdengapp.SectionPlugin

// seriesOverviewProbe reports the boolean overview verdict for (series, lang).
// *SeriesOverviewStalenessHolder satisfies it (dormant/unbound at runtime).
type seriesOverviewProbe interface {
	OverviewStale(ctx context.Context, seriesID domain.SeriesID, lang string) (stale bool, reason string, err error)
}

// seriesAllLangsRefresher drives SeriesWorker.RefreshSeriesAllLangs.
// *SeriesAllLangsRefresherHolder satisfies it.
type seriesAllLangsRefresher interface {
	RefreshSeriesAllLangs(ctx context.Context, seriesID domain.SeriesID) error
}

// seriesTextPlugin implements the engine SectionPlugin for (series, text). Series
// text/overview freshness is boolean (EnrichmentTextSyncedAt TTL + missing-lang, per
// seriesdetail probe.go:240-248), so it rides the STALENESS arm and NO-OPs Coverage —
// symmetric with movieTextPlugin (see Decision D-arm). Registered but DORMANT: no
// runtime code drives the engine with a series MediaID, so the series live path is
// byte-identical. Refresh delegates to SeriesWorker.RefreshSeriesAllLangs (all-langs,
// idempotent; lang arg intentionally dropped).
type seriesTextPlugin struct {
	overview seriesOverviewProbe
	refresh  seriesAllLangsRefresher
}

// NewSeriesTextPlugin constructs the (series, text) plugin.
func NewSeriesTextPlugin(overview seriesOverviewProbe, refresh seriesAllLangsRefresher) mdengSectionPlugin {
	return &seriesTextPlugin{overview: overview, refresh: refresh}
}

// Section is the canonical text section.
func (p *seriesTextPlugin) Section() mdengdomain.Section { return mdengdomain.SectionText }

// Coverage NO-OP: series text is boolean-shaped.
func (p *seriesTextPlugin) Coverage(context.Context, mdengdomain.MediaID, string) (int, int, error) {
	return 0, 0, nil
}

// Staleness returns the boolean overview verdict. Read error → (verdict, err);
// engine assess() fails CLOSED.
func (p *seriesTextPlugin) Staleness(ctx context.Context, id mdengdomain.MediaID, lang string, _ time.Time) (mdengdomain.SectionVerdict, error) {
	stale, reason, err := p.overview.OverviewStale(ctx, domain.SeriesID(id.InternalID()), lang)
	if err != nil {
		return mdengdomain.SectionVerdict{Section: mdengdomain.SectionText}, err
	}
	return mdengdomain.SectionVerdict{Section: mdengdomain.SectionText, Stale: stale, Reason: reason}, nil
}

// Refresh drives the all-langs series text refresh.
func (p *seriesTextPlugin) Refresh(ctx context.Context, id mdengdomain.MediaID, _ string) error {
	return p.refresh.RefreshSeriesAllLangs(ctx, domain.SeriesID(id.InternalID()))
}
