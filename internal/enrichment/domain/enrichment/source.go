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
type Kind string

const (
	KindUnknown          Kind = ""
	KindSeriesContinuing Kind = "series_continuing"
	KindSeriesEnded      Kind = "series_ended"
	KindSeasonActive     Kind = "season_active"
	KindSeasonClosed     Kind = "season_closed"
	KindPerson           Kind = "person"
	KindOMDb             Kind = "omdb"
)

// IsValid reports whether k is one of the six known kinds.
// KindUnknown is NOT valid — callers MUST classify before
// passing to TTL.
func (k Kind) IsValid() bool {
	switch k {
	case KindSeriesContinuing, KindSeriesEnded,
		KindSeasonActive, KindSeasonClosed,
		KindPerson, KindOMDb:
		return true
	}
	return false
}
