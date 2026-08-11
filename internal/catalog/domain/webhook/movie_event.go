package webhook

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieEventType is the domain classification of a Radarr Connect webhook after
// the infra DTO is parsed. Mirror of EventType for the movie vertical.
type MovieEventType string

const (
	// MovieEventTypeUpsert — MovieAdded / Grab / Download(import) / Rename /
	// MovieFileImported / MovieFileDelete: the on-disk / membership state of a
	// movie changed; refresh the movie_states + movies-canon cache. HasFile is
	// derived from the raw event (import → true, file-delete → false).
	MovieEventTypeUpsert MovieEventType = "movie_upsert"

	// MovieEventTypeDeleted — MovieDelete: operator removed the movie; soft-
	// delete the movie_states row (mirror EventTypeSeriesDeleted).
	MovieEventTypeDeleted MovieEventType = "movie_deleted"

	// MovieEventTypeUnsupported — Test / Health / ApplicationUpdate /
	// ManualInteractionRequired / HealthRestored / unknown. No-op 200.
	MovieEventTypeUnsupported MovieEventType = "movie_unsupported"
)

// MovieEvent is the domain projection of a Radarr webhook payload. Plain Go
// types only — no JSON/DB tags, no Radarr knowledge. InstanceName comes from
// the URL path param, NOT the payload (mirror of Event). The app-layer
// MovieUseCase assembles ports.RadarrMovie from these fields and calls the
// shared F-21 helper, so the domain never imports shared/dataports.
type MovieEvent struct {
	Type         MovieEventType
	InstanceName domain.InstanceName

	RadarrMovieID int
	Title         string
	TitleSlug     string
	Year          int
	TMDBID        int
	IMDBID        string

	// HasFile is the derived on-disk state for this event (import → true,
	// file-delete → false, MovieAdded → false). Monitored defaults true on
	// MovieAdded; passthrough of payload movie.monitored otherwise.
	HasFile   bool
	Monitored bool

	// SizeBytes is the movieFile size on an import event; 0 = absent. Only the
	// RICH sync writer persists size — the webhook uses UpsertStub which omits
	// it — so this is informational for logging today.
	SizeBytes int64

	// MinimumAvailability from the payload movie object; "" = absent.
	MinimumAvailability string

	OccurredAt   time.Time
	RawEventType string
}
