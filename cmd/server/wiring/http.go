package wiring

import (
	"log/slog"

	"github.com/alexmorbo/seasonfill/application/ports"
	httpserver "github.com/alexmorbo/seasonfill/interface/http"
)

// BuildHTTPServer wraps the 37-arg httpserver.NewServer invocation that
// previously lived inline in cmd/server/server.go (B-11 step 20 / story
// 342).
//
// Positional arg ORDER is the immutable contract here: the underlying
// httpserver.NewServer signature is the canonical positional list and
// THIS wrapper does NOT reorder, group, or drop. Every argument is
// read straight off a bundle (or one of the two named locals) and
// forwarded in the same slot.
//
// seriesCacheRepo + counterRepo come in as explicit parameters because
// they are not (yet) members of any existing bundle — they are
// constructed inline in server.go (alongside seriesRepo) and used both
// by the HTTP server and by the enrichment block below it. Pushing
// them onto a bundle is out of scope for B-11 step 20.
//
// The LATE BIND ZONE in server.go runs AFTER this wirer is called — the
// handlers passed in are pointer-typed, so mutations applied to
// mediaBundle.Handler (SetOnDemandFetcher) and
// seriesDetailBundle.MediaResolver (SetSideEffects) after this
// constructor returns are visible at request time via gin's per-handler
// dispatch. The pre-342 layout called httpserver.NewServer at the same
// position, so this wirer preserves that ordering verbatim.
func BuildHTTPServer(
	persistence *PersistenceBundle,
	runtimecfg *RuntimeConfigBundle,
	auth *AuthBundle,
	sonarrBundle *SonarrBundle,
	watchdogBundle *WatchdogBundle,
	scanBundle *ScanBundle,
	webhookBundle *WebhookBundle,
	instanceBundle *InstanceBundle,
	regrabBundle *RegrabBundle,
	torrentsyncBundle *TorrentsyncBundle,
	extSvcBundle *ExtSvcBundle,
	mediaBundle *MediaBundle,
	seriesDetailBundle *SeriesDetailBundle,
	seriesCacheRepo ports.SeriesCacheRepository,
	counterRepo ports.CounterRepository,
	log *slog.Logger,
) *httpserver.Server {
	return httpserver.NewServer(
		runtimecfg.ServeConfig.HTTP,
		scanBundle.ScanUC,
		webhookBundle.WebhookUC,
		watchdogBundle.Checker,
		scanBundle.ScanRepo,
		scanBundle.DecisionRepo,
		scanBundle.GrabRepo,
		auth.AdminRepo,
		auth.LoginLimiter,
		auth.WebhookLimiter,
		sonarrBundle.InstanceReg,
		scanBundle.CooldownRepo,
		scanBundle.GrabUC,
		scanBundle.RescanUC,
		instanceBundle.CRUDHandler,
		instanceBundle.ProbeHandler,
		runtimecfg.Handler,
		regrabBundle.QbitSettingsHandler,
		extSvcBundle.Handler,
		auth.OIDCUC,
		webhookBundle.Reconciler,
		webhookBundle.StatusCache,
		seriesCacheRepo,
		counterRepo,
		regrabBundle.WatchdogRollupHandler,
		regrabBundle.WatchdogBlacklistHandler,
		regrabBundle.WatchdogSeasonsHandler,
		regrabBundle.WebhooksAggregateHandler,
		mediaBundle.Handler,
		mediaBundle.AssetsRepo,
		seriesDetailBundle.DetailHandler,
		seriesDetailBundle.SeasonHandler,
		seriesDetailBundle.CastHandler,
		seriesDetailBundle.PeopleHandler,
		seriesDetailBundle.RefreshHandler,
		torrentsyncBundle.SeriesTorrentsHandler,
		persistence.TimezoneHandler,
		log,
	)
}
