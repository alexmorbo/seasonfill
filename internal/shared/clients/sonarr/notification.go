package sonarr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// downloadClientDTO mirrors the subset of Sonarr's
// /api/v3/downloadclient response we need for qBit auto-discover.
// Password is intentionally NOT decoded — Sonarr redacts it server
// side via the `privacy:"password"` annotation; the wire payload
// either omits the field or carries a placeholder.
type downloadClientDTO struct {
	ID             int                   `json:"id"`
	Name           string                `json:"name"`
	Implementation string                `json:"implementation"`
	Enable         bool                  `json:"enable"`
	Fields         []downloadClientField `json:"fields"`
}

// downloadClientField mirrors Sonarr's field-array entries on
// /downloadclient. We pluck host, port, username, category from this
// by `name` rather than decoding into a typed struct because Sonarr's
// download-client field set varies per implementation.
type downloadClientField struct {
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

// DownloadClient is the trimmed, typed shape ListDownloadClients
// returns to the discover handler.
type DownloadClient struct {
	ID             int
	Name           string
	Implementation string
	Enable         bool
	Host           string
	Port           int
	Username       string
	Category       string
}

// notificationDTO mirrors Sonarr's /api/v3/notification response
// shape (subset). Fields are preserved verbatim as []NotificationField
// so the create path can mirror them when building a new Webhook
// notification — see CreateNotification.
type notificationDTO struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Implementation string `json:"implementation"`
	ConfigContract string `json:"configContract,omitempty"`
	OnGrab         bool   `json:"onGrab"`
	OnDownload     bool   `json:"onDownload"`
	// OnDownloadFailure / OnImportFailure are legacy v3 aliases for the
	// import-failed signal. Modern Sonarr (v4) exposes the trigger as
	// OnManualInteractionRequired; the legacy keys are unknown to it and
	// silently ignored (ASP.NET Core skips unmapped members), so we send
	// all three to cover every Sonarr version. OnDownloadFailure is kept
	// without omitempty so the version fallback still emits it for v3.
	OnDownloadFailure           bool                `json:"onDownloadFailure"`
	OnImportFailure             bool                `json:"onImportFailure,omitempty"`
	OnManualInteractionRequired bool                `json:"onManualInteractionRequired,omitempty"`
	OnEpisodeFileDelete         bool                `json:"onEpisodeFileDelete,omitempty"`
	OnSeriesAdd                 bool                `json:"onSeriesAdd,omitempty"`
	OnSeriesDelete              bool                `json:"onSeriesDelete,omitempty"`
	Fields                      []NotificationField `json:"fields"`
}

// NotificationField is the field-array entry shape on
// /api/v3/notification. Value is preserved as any so JSON
// numbers, strings, and bools round-trip without coercion.
type NotificationField struct {
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

// Notification is the trimmed, typed shape Sonarr-list methods return.
// `Fields` is preserved verbatim from the wire payload so callers can
// match by url and so CreateNotification can mirror the field shape
// when building a new Webhook (defends against per-Sonarr-version
// shape variance — see Concerns §2).
type Notification struct {
	ID                          int
	Name                        string
	Implementation              string
	OnGrab                      bool
	OnDownload                  bool
	OnDownloadFailure           bool
	OnManualInteractionRequired bool
	OnEpisodeFileDelete         bool
	Fields                      []NotificationField
}

// NotificationPayload carries only what callers must supply when
// asking us to create a Webhook notification. The full Sonarr payload
// (configContract, implementationName, on-event triggers) is
// hardcoded inside CreateNotification.
type NotificationPayload struct {
	Name         string
	URL          string
	APIKeyHeader string // populated as the X-Api-Key header value
	// TemplateFields, if non-nil, mirrors the field shape of an
	// existing Webhook notification so the new one matches Sonarr's
	// current schema. CreateNotification substitutes url + headers
	// in-place and leaves every other field untouched. nil means use
	// the minimal known-good template.
	TemplateFields []NotificationField
}

// ListDownloadClients calls Sonarr GET /api/v3/downloadclient and
// returns the trimmed DownloadClient slice. The host/port/username/
// category lookup is best-effort: missing fields yield zero values.
func (c *Client) ListDownloadClients(ctx context.Context) ([]DownloadClient, error) {
	var dtos []downloadClientDTO
	if err := c.get(ctx, "/api/v3/downloadclient", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]DownloadClient, 0, len(dtos))
	for _, d := range dtos {
		dc := DownloadClient{
			ID: d.ID, Name: d.Name,
			Implementation: d.Implementation, Enable: d.Enable,
		}
		for _, f := range d.Fields {
			switch f.Name {
			case "host":
				if s, ok := f.Value.(string); ok {
					dc.Host = s
				}
			case "port":
				dc.Port = toInt(f.Value)
			case "username":
				if s, ok := f.Value.(string); ok {
					dc.Username = s
				}
			case "category", "tvCategory":
				if s, ok := f.Value.(string); ok {
					dc.Category = s
				}
			}
		}
		out = append(out, dc)
	}
	return out, nil
}

// ListNotifications calls Sonarr GET /api/v3/notification and returns
// the trimmed Notification slice. Fields are preserved verbatim for
// the install-handler match-by-url loop and for shape mirroring in
// CreateNotification.
func (c *Client) ListNotifications(ctx context.Context) ([]Notification, error) {
	var dtos []notificationDTO
	if err := c.get(ctx, "/api/v3/notification", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, notificationFromDTO(d))
	}
	return out, nil
}

// notificationFromDTO projects the wire DTO onto the trimmed typed
// Notification. OnDownloadFailure folds the legacy v3 alias with the
// v4 ManualInteractionRequired trigger so callers see a single
// "import-failed enabled" signal regardless of Sonarr version.
func notificationFromDTO(d notificationDTO) Notification {
	return Notification{
		ID: d.ID, Name: d.Name, Implementation: d.Implementation,
		OnGrab: d.OnGrab, OnDownload: d.OnDownload,
		OnDownloadFailure:           d.OnDownloadFailure || d.OnImportFailure || d.OnManualInteractionRequired,
		OnManualInteractionRequired: d.OnManualInteractionRequired,
		OnEpisodeFileDelete:         d.OnEpisodeFileDelete,
		Fields:                      d.Fields,
	}
}

// CreateNotification POSTs a Webhook notification to Sonarr and
// returns the created Notification. The payload mirrors any
// TemplateFields supplied by the caller; otherwise a minimal
// known-good template is used.
func (c *Client) CreateNotification(ctx context.Context, p NotificationPayload) (Notification, error) {
	body := notificationDTO{
		Name:           p.Name,
		Implementation: "Webhook",
		ConfigContract: "WebhookSettings",
		Fields:         buildNotificationFields(p),
	}
	setDesiredTriggers(&body)
	resp, err := c.submitNotification(ctx, false, "/api/v3/notification", body)
	if err != nil {
		return Notification{}, err
	}
	return notificationFromDTO(resp), nil
}

// UpdateNotification PUTs an existing Webhook notification by ID,
// rewriting `url` + `headers` while preserving any other field the
// caller carried in `existing.Fields` (version-variance defence —
// same rationale as CreateNotification mirroring an existing entry).
// The full consumed-event trigger set is also re-applied so a
// notification created by an older seasonfill (fewer triggers) is
// upgraded in place on the next reconcile. `existing.ID` is reused
// verbatim so Sonarr matches the row.
func (c *Client) UpdateNotification(ctx context.Context, existing Notification, p NotificationPayload) (Notification, error) {
	if existing.ID == 0 {
		return Notification{}, fmt.Errorf("update notification: missing id")
	}
	merged := NotificationPayload{
		Name: p.Name, URL: p.URL, APIKeyHeader: p.APIKeyHeader,
		TemplateFields: existing.Fields,
	}
	body := notificationDTO{
		ID:             existing.ID,
		Name:           p.Name,
		Implementation: "Webhook",
		ConfigContract: "WebhookSettings",
		Fields:         buildNotificationFields(merged),
	}
	setDesiredTriggers(&body)
	endpoint := "/api/v3/notification/" + strconv.Itoa(existing.ID)
	resp, err := c.submitNotification(ctx, true, endpoint, body)
	if err != nil {
		return Notification{}, err
	}
	return notificationFromDTO(resp), nil
}

// setDesiredTriggers turns on exactly the Sonarr notification triggers
// whose events seasonfill consumes (webhook.EventType.IsConsumed):
//
//	Grabbed             -> onGrab
//	Imported            -> onDownload
//	ImportFailed        -> onManualInteractionRequired (v4)
//	                       + onDownloadFailure/onImportFailure (v3 legacy)
//	EpisodeFileDelete   -> onEpisodeFileDelete
//	SeriesAdd           -> onSeriesAdd
//	SeriesDeleted       -> onSeriesDelete
//
// Unsupported events (Rename/FileUpgrade/ImportComplete/Health*/
// AppUpdate) are deliberately NOT requested. This is the single source
// of the desired trigger set — Create and Update both call it so they
// cannot drift.
func setDesiredTriggers(dto *notificationDTO) {
	dto.OnGrab = true
	dto.OnDownload = true
	dto.OnManualInteractionRequired = true
	dto.OnDownloadFailure = true
	dto.OnImportFailure = true
	dto.OnEpisodeFileDelete = true
	dto.OnSeriesAdd = true
	dto.OnSeriesDelete = true
}

// dropUnsupportedTriggers strips the triggers an older Sonarr may not
// recognise, leaving the Phase 10 core (onGrab/onDownload/
// onDownloadFailure). Used by the version-variance fallback after a
// 400 whose body names one of the newer trigger fields. The dropped
// fields carry omitempty so they vanish from the retried payload.
func dropUnsupportedTriggers(dto *notificationDTO) {
	dto.OnSeriesAdd = false
	dto.OnSeriesDelete = false
	dto.OnEpisodeFileDelete = false
	dto.OnManualInteractionRequired = false
}

// submitNotification POSTs (isPut=false) or PUTs (isPut=true) the
// notification body, retrying once without the newer trigger fields
// when Sonarr rejects them (isUnsupportedTriggerErr). All other errors
// propagate. Shared by Create and Update so the fallback logic lives in
// one place.
func (c *Client) submitNotification(ctx context.Context, isPut bool, endpoint string, body notificationDTO) (notificationDTO, error) {
	send := func(b notificationDTO, resp *notificationDTO) error {
		if isPut {
			return c.put(ctx, endpoint, b, resp)
		}
		return c.post(ctx, endpoint, b, resp)
	}
	var resp notificationDTO
	if err := send(body, &resp); err != nil {
		if !isUnsupportedTriggerErr(err) {
			return notificationDTO{}, err
		}
		c.logger.WarnContext(ctx, "sonarr_notification_unsupported_triggers_fallback",
			slog.String("instance", string(c.name)),
			slog.String("error", err.Error()),
		)
		dropUnsupportedTriggers(&body)
		if err2 := send(body, &resp); err2 != nil {
			return notificationDTO{}, err2
		}
	}
	return resp, nil
}

// DeleteNotification removes the Sonarr webhook entry by ID. Used on
// instance delete to keep Sonarr's notification list clean. Caller
// treats errors as best-effort (log + continue).
func (c *Client) DeleteNotification(ctx context.Context, id int) error {
	if id == 0 {
		return fmt.Errorf("delete notification: missing id")
	}
	return c.delete(ctx, "/api/v3/notification/"+strconv.Itoa(id))
}

// WebhookFieldURL extracts the raw URL string from a notification's
// fields array. Returns "" when absent or not a string. Shared by
// the reconciler + status handler.
func WebhookFieldURL(fields []NotificationField) string {
	for _, f := range fields {
		if f.Name != "url" {
			continue
		}
		if s, ok := f.Value.(string); ok {
			return s
		}
		return ""
	}
	return ""
}

// isUnsupportedTriggerErr returns true when Sonarr rejected the write
// body specifically because one of the newer trigger fields
// (SeriesAdd / SeriesDelete / EpisodeFileDelete /
// ManualInteractionRequired) is unknown to it (older Sonarr). Rule:
// HTTP 400 with body containing the trigger name (case-insensitive).
// All other failure modes — network, auth, 5xx, other 4xx — return
// false so they propagate.
func isUnsupportedTriggerErr(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	if se.Status != 400 {
		return false
	}
	body := strings.ToLower(se.Body)
	return strings.Contains(body, "onseriesadd") ||
		strings.Contains(body, "onseriesdelete") ||
		strings.Contains(body, "onepisodefiledelete") ||
		strings.Contains(body, "onmanualinteractionrequired")
}

// buildNotificationFields constructs the Sonarr notification.fields
// array. If TemplateFields is supplied, url and headers are
// substituted in-place; every other entry is preserved verbatim.
// Otherwise a minimal known-good template is emitted. This is the
// version-variance defence — Sonarr's field schema drifts across
// versions, and mirroring an existing webhook is the most defensive
// shape we can produce.
func buildNotificationFields(p NotificationPayload) []NotificationField {
	// Sonarr v3 expects headers as an array of {key, value} objects
	// (IEnumerable<KeyValuePair<string,string>>), not a plain string.
	headersValue := []map[string]string{{"key": "X-Api-Key", "value": p.APIKeyHeader}}
	if len(p.TemplateFields) > 0 {
		out := make([]NotificationField, 0, len(p.TemplateFields))
		urlSet, headersSet := false, false
		for _, f := range p.TemplateFields {
			switch f.Name {
			case "url":
				out = append(out, NotificationField{Name: "url", Value: p.URL})
				urlSet = true
			case "headers":
				out = append(out, NotificationField{Name: "headers", Value: headersValue})
				headersSet = true
			default:
				out = append(out, f)
			}
		}
		if !urlSet {
			out = append(out, NotificationField{Name: "url", Value: p.URL})
		}
		if !headersSet {
			out = append(out, NotificationField{Name: "headers", Value: headersValue})
		}
		return out
	}
	return []NotificationField{
		{Name: "url", Value: p.URL},
		{Name: "method", Value: 1},
		{Name: "username", Value: ""},
		{Name: "password", Value: ""},
		{Name: "headers", Value: headersValue},
	}
}

// toInt is a lenient JSON-number → int coercion. Sonarr emits port as
// either a JSON number (float64 after decode) or an int-shaped string
// depending on field type; we tolerate both.
func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}
