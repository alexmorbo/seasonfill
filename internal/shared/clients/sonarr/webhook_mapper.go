package sonarr

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ErrMalformedPayload — JSON body unparseable or missing eventType.
// 007c handler maps to HTTP 400. There is intentionally no
// ErrUnsupportedEventType — see Note 2 in the story body.
var ErrMalformedPayload = errors.New("malformed webhook payload")

// webhookEventAlias maps known Sonarr eventType strings to domain
// EventType. Case-insensitive (see classifyEventType). v3 aliases
// (DownloadFailure / ImportFailure) accepted alongside v4 canonical
// ManualInteractionRequired per Q-6. Recognised-but-unused events are
// listed explicitly so the alias-map test catches regressions; any
// missing key falls through to EventTypeUnsupported by default.
var webhookEventAlias = map[string]webhook.EventType{
	"grab":                      webhook.EventTypeGrabbed,
	"download":                  webhook.EventTypeImported,
	"import":                    webhook.EventTypeImported,
	"manualinteractionrequired": webhook.EventTypeImportFailed,
	"downloadfailure":           webhook.EventTypeImportFailed,
	"importfailure":             webhook.EventTypeImportFailed,
	"test":                      webhook.EventTypeUnsupported,
	"rename":                    webhook.EventTypeUnsupported,
	"health":                    webhook.EventTypeUnsupported,
	"healthrestored":            webhook.EventTypeUnsupported,
	"applicationupdate":         webhook.EventTypeUnsupported,
	"seriesadd":                 webhook.EventTypeSeriesAdd,
	"seriesdelete":              webhook.EventTypeSeriesDeleted,
	"episodefiledelete":         webhook.EventTypeEpisodeFileDelete,
}

// MapWebhookEvent parses a Sonarr webhook payload and projects it onto
// a domain Event. instanceName comes from the URL path param (Q-8);
// the payload's own instanceName field is operator-set and ignored.
// Returns ErrMalformedPayload (wrapped) on JSON parse failure or
// missing eventType. Unknown event types are NOT errors — they return
// (Event{Type: EventTypeUnsupported, ...}, nil).
func MapWebhookEvent(payload []byte, instanceName domain.InstanceName) (webhook.Event, error) {
	if len(payload) == 0 {
		return webhook.Event{}, fmt.Errorf("%w: empty body", ErrMalformedPayload)
	}
	var dto webhookPayloadDTO
	if err := json.Unmarshal(payload, &dto); err != nil {
		return webhook.Event{}, fmt.Errorf("%w: json decode: %w", ErrMalformedPayload, err)
	}
	if strings.TrimSpace(dto.EventType) == "" {
		return webhook.Event{}, fmt.Errorf("%w: missing eventType", ErrMalformedPayload)
	}

	classified := classifyEventType(dto.EventType)

	ev := webhook.Event{
		Type:         classified,
		InstanceName: instanceName,
		DownloadID:   dto.DownloadID,
		RawEventType: dto.EventType,
		OccurredAt:   coalesceTime(dto.EventTimestamp),
	}
	if dto.Release != nil {
		ev.ReleaseTitle = dto.Release.ReleaseTitle
		ev.Indexer = dto.Release.Indexer
		ev.ReleaseSize = dto.Release.Size
	}
	if dto.Series != nil {
		ev.SeriesID = dto.Series.ID
		ev.SeriesTitle = dto.Series.Title
		ev.SeriesTitleSlug = dto.Series.TitleSlug
		ev.SeriesTVDBID = dto.Series.TvdbID
		ev.SeriesIMDBID = dto.Series.ImdbID
	}
	if len(dto.Episodes) > 0 {
		ev.SeasonNumber = dto.Episodes[0].SeasonNumber
	}
	if classified == webhook.EventTypeImportFailed {
		ev.Message = joinStatusMessages(dto.DownloadStatusMessages)
	}
	return ev, nil
}

// classifyEventType resolves a raw eventType through the alias map
// (case-insensitive). Unknown values default to Unsupported.
func classifyEventType(raw string) webhook.EventType {
	key := strings.ToLower(strings.TrimSpace(raw))
	if t, ok := webhookEventAlias[key]; ok {
		return t
	}
	return webhook.EventTypeUnsupported
}

// coalesceTime returns t (UTC) if non-nil and non-zero, else time.Now
// (UTC). The mapper's only impure call site.
func coalesceTime(t *time.Time) time.Time {
	if t != nil && !t.IsZero() {
		return t.UTC()
	}
	return time.Now().UTC()
}

// joinStatusMessages flattens Sonarr's TrackedDownloadStatusMessage list
// into a newline-separated string. Empty entries are skipped.
func joinStatusMessages(msgs []webhookStatusMessageDTO) string {
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range msgs {
		title := strings.TrimSpace(m.Title)
		for _, line := range m.Messages {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			if title != "" {
				b.WriteString(title)
				b.WriteString(": ")
			}
			b.WriteString(line)
		}
	}
	return b.String()
}
