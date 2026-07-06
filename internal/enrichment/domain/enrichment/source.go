package enrichment

// Kind discriminates the lifecycle bucket of a hydration target
// for the TTL matrix (PRD v4 §5.5). The same Source can return
// different TTLs depending on the lifecycle kind — a TMDB series
// that is still in production refreshes every 24h, while an ended
// one refreshes every 30 days. The composer / dispatcher decides
// which Kind applies (it reads from `series.Canon.Status` for
// series; from a season's `air_date` recency for seasons) and
// passes the value explicitly to TTL — Kind is never derived
// inside ttl.go (keeps the function table-driven and pure).
//
// KindUnknown is the defensive default for callers that have not
// yet classified an entity; TTL returns 0 ("no TTL — treat as
// live source") for it so a misclassification surfaces as
// over-eager refresh, not as a stuck stale row.
//
// The KindOMDb* age-subtypes (W18-5) drive a progressive OMDb TTL
// curve (2d/7d/30d/90d/180d) so old finished series aren't polled
// daily against the OMDb free-tier quota. The classifier lives in
// the OMDb worker (app.classifyOMDbKind) and the batch selector's
// SQL age-CASE — ttl.go stays pure/table-driven. The base KindOMDb
// (24h) is KEPT unchanged: the series-detail composer
// (seriesdetail/app/composer.go:730) still maps SourceOMDb→KindOMDb
// for its degraded/freshness projection and must not shift.
type Kind string

const (
	KindUnknown          Kind = ""
	KindSeriesContinuing Kind = "series_continuing"
	KindSeriesEnded      Kind = "series_ended"
	KindSeasonActive     Kind = "season_active"
	KindSeasonClosed     Kind = "season_closed"
	KindPerson           Kind = "person"
	KindOMDb             Kind = "omdb"

	// W18-5 progressive OMDb age-subtypes. KindOMDb (24h) is retained
	// for the composer; these are additive and consumed by the OMDb
	// worker's in-band staleness guard.
	KindOMDbInProduction Kind = "omdb_in_production" // 2d
	KindOMDbRecent       Kind = "omdb_recent"        // 7d  (last air < 1y)
	KindOMDbMid          Kind = "omdb_mid"           // 30d (1y–3y, or unknown age)
	KindOMDbOld          Kind = "omdb_old"           // 90d (3y–8y)
	KindOMDbAncient      Kind = "omdb_ancient"       // 180d (> 8y)
)

// IsValid reports whether k is one of the known kinds.
// KindUnknown is NOT valid — callers MUST classify before
// passing to TTL.
func (k Kind) IsValid() bool {
	switch k {
	case KindSeriesContinuing, KindSeriesEnded,
		KindSeasonActive, KindSeasonClosed,
		KindPerson, KindOMDb,
		KindOMDbInProduction, KindOMDbRecent,
		KindOMDbMid, KindOMDbOld, KindOMDbAncient:
		return true
	}
	return false
}
