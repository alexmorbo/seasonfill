package radarr

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ErrMalformedPayload — JSON body unparseable or missing eventType. The REST
// handler maps to HTTP 400. Mirror of sonarr.ErrMalformedPayload.
var ErrMalformedPayload = errors.New("malformed radarr webhook payload")

// movieWebhookAlias maps Radarr eventType strings → domain MovieEventType.
// Case-insensitive. Unknown keys fall through to MovieEventTypeUnsupported.
//
// "grab" is classified DISTINCTLY (ADR-0023 B1.2): it is the only Radarr
// event carrying downloadId, and it must NOT drive the movie_states cache
// write (its hasFile is always false — see MovieEventTypeUpsert's docstring).
var movieWebhookAlias = map[string]webhook.MovieEventType{
	"movieadded":                webhook.MovieEventTypeUpsert,
	"grab":                      webhook.MovieEventTypeGrabbed, // B1.2 — carries downloadId
	"download":                  webhook.MovieEventTypeUpsert,  // import success
	"moviefileimported":         webhook.MovieEventTypeUpsert,
	"rename":                    webhook.MovieEventTypeUpsert,
	"moviefiledelete":           webhook.MovieEventTypeUpsert, // has_file → false
	"moviedelete":               webhook.MovieEventTypeDeleted,
	"test":                      webhook.MovieEventTypeUnsupported,
	"health":                    webhook.MovieEventTypeUnsupported,
	"healthrestored":            webhook.MovieEventTypeUnsupported,
	"applicationupdate":         webhook.MovieEventTypeUnsupported,
	"manualinteractionrequired": webhook.MovieEventTypeUnsupported,
}

// MapWebhookEvent parses a Radarr webhook payload → domain MovieEvent.
// instanceName comes from the URL path param (the payload's own instanceName is
// operator-set and ignored). ErrMalformedPayload on JSON failure / missing
// eventType. Unknown event types are NOT errors (→ Unsupported). Mirror of
// sonarr.MapWebhookEvent. Signature matches the drainer's radarr MapFunc.
func MapWebhookEvent(payload []byte, instanceName domain.InstanceName) (webhook.MovieEvent, error) {
	if len(payload) == 0 {
		return webhook.MovieEvent{}, fmt.Errorf("%w: empty body", ErrMalformedPayload)
	}
	var dto webhookPayloadDTO
	if err := json.Unmarshal(payload, &dto); err != nil {
		return webhook.MovieEvent{}, fmt.Errorf("%w: json decode: %w", ErrMalformedPayload, err)
	}
	if strings.TrimSpace(dto.EventType) == "" {
		return webhook.MovieEvent{}, fmt.Errorf("%w: missing eventType", ErrMalformedPayload)
	}

	raw := strings.ToLower(strings.TrimSpace(dto.EventType))
	classified, ok := movieWebhookAlias[raw]
	if !ok {
		classified = webhook.MovieEventTypeUnsupported
	}

	ev := webhook.MovieEvent{
		Type:         classified,
		InstanceName: instanceName,
		RawEventType: dto.EventType,
		OccurredAt:   coalesceTime(dto.EventTimestamp),
		Monitored:    true, // default; overridden by movie.monitored when present
		// B1.2: only Grab populates downloadId; passthrough unconditionally so
		// the domain — not this mapper — decides what to do with it.
		DownloadID: strings.TrimSpace(dto.DownloadID),
	}
	if dto.Movie != nil {
		ev.RadarrMovieID = dto.Movie.ID
		ev.Title = dto.Movie.Title
		ev.TitleSlug = dto.Movie.TitleSlug
		ev.Year = dto.Movie.Year
		ev.TMDBID = dto.Movie.TMDBID
		ev.IMDBID = dto.Movie.IMDBID
		ev.MinimumAvailability = dto.Movie.MinimumAvailability
		if dto.Movie.Monitored != nil {
			ev.Monitored = *dto.Movie.Monitored
		}
	}
	// HasFile derivation: import events set it true, file-delete false, else
	// default false.
	switch raw {
	case "download", "moviefileimported":
		ev.HasFile = true
	case "moviefiledelete":
		ev.HasFile = false
	default:
		ev.HasFile = false
	}
	if dto.MovieFile != nil && dto.MovieFile.Size > 0 {
		ev.SizeBytes = dto.MovieFile.Size
	}
	return ev, nil
}

func coalesceTime(t *time.Time) time.Time {
	if t != nil && !t.IsZero() {
		return t.UTC()
	}
	return time.Now().UTC()
}
