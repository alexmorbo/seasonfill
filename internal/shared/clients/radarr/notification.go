package radarr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// notificationDTO mirrors Radarr's /api/v3/notification response shape
// (subset). Fields are preserved verbatim as []NotificationField so the create
// path can mirror them when building a new Webhook notification — see
// CreateNotification. Mirror of sonarr.notificationDTO with the MOVIE trigger
// flags radarr's notification resource accepts (onMovieAdded/onMovieDelete/
// onMovieFileDelete/onManualInteractionRequired/onHealthIssue) instead of
// sonarr's series/episode flags.
//
// OnGrab and OnDownload carry NO omitempty — they are the minimal known-good
// core the version-variance fallback keeps. The newer/optional triggers carry
// omitempty so dropUnsupportedTriggers makes them vanish from the retried
// payload.
type notificationDTO struct {
	ID                          int                 `json:"id"`
	Name                        string              `json:"name"`
	Implementation              string              `json:"implementation"`
	ConfigContract              string              `json:"configContract,omitempty"`
	OnGrab                      bool                `json:"onGrab"`
	OnDownload                  bool                `json:"onDownload"`
	OnMovieAdded                bool                `json:"onMovieAdded,omitempty"`
	OnMovieDelete               bool                `json:"onMovieDelete,omitempty"`
	OnMovieFileDelete           bool                `json:"onMovieFileDelete,omitempty"`
	OnRename                    bool                `json:"onRename,omitempty"`
	OnManualInteractionRequired bool                `json:"onManualInteractionRequired,omitempty"`
	OnHealthIssue               bool                `json:"onHealthIssue,omitempty"`
	Fields                      []NotificationField `json:"fields"`
}

// NotificationField is the field-array entry shape on /api/v3/notification.
// Value is preserved as any so JSON numbers, strings, and bools round-trip
// without coercion. Mirror of sonarr.NotificationField.
type NotificationField struct {
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

// Notification is the trimmed, typed shape radarr-list methods return. Fields
// is preserved verbatim so callers can match by url and so CreateNotification
// can mirror the field shape when building a new Webhook (version-variance
// defence). The On* bools are the readable trigger flags used for drift
// detection by the (M-FIX-4b) webhook reconciler. Mirror of sonarr.Notification
// with the movie trigger set.
type Notification struct {
	ID                          int
	Name                        string
	Implementation              string
	OnGrab                      bool
	OnDownload                  bool
	OnMovieAdded                bool
	OnMovieDelete               bool
	OnMovieFileDelete           bool
	OnRename                    bool
	OnManualInteractionRequired bool
	OnHealthIssue               bool
	Fields                      []NotificationField
}

// TriggerSet is the readable subset of a notification's on-event trigger flags
// the reconciler compares for drift. Being a comparable value type, two
// TriggerSets can be tested with ==. Mirror of sonarr.TriggerSet.
type TriggerSet struct {
	OnGrab                      bool
	OnDownload                  bool
	OnMovieAdded                bool
	OnMovieDelete               bool
	OnMovieFileDelete           bool
	OnRename                    bool
	OnManualInteractionRequired bool
	OnHealthIssue               bool
}

// Triggers projects a Notification onto its comparable TriggerSet.
func (n Notification) Triggers() TriggerSet {
	return TriggerSet{
		OnGrab:                      n.OnGrab,
		OnDownload:                  n.OnDownload,
		OnMovieAdded:                n.OnMovieAdded,
		OnMovieDelete:               n.OnMovieDelete,
		OnMovieFileDelete:           n.OnMovieFileDelete,
		OnRename:                    n.OnRename,
		OnManualInteractionRequired: n.OnManualInteractionRequired,
		OnHealthIssue:               n.OnHealthIssue,
	}
}

// NotificationPayload carries only what callers must supply when asking us to
// create a Webhook notification. The full radarr payload (configContract,
// on-event triggers) is hardcoded inside CreateNotification. Mirror of
// sonarr.NotificationPayload.
type NotificationPayload struct {
	Name         string
	URL          string
	APIKeyHeader string // populated as the X-Api-Key header value
	// TemplateFields, if non-nil, mirrors the field shape of an existing
	// Webhook notification so the new one matches radarr's current schema.
	// CreateNotification substitutes url + headers in-place and leaves every
	// other field untouched. nil means use the minimal known-good template.
	TemplateFields []NotificationField
}

// downloadClientDTO mirrors the subset of Radarr's /api/v3/downloadclient
// response we need for qBit auto-discover. Radarr v3 exposes the identical
// endpoint + field-array shape as Sonarr. Password is intentionally NOT decoded
// — Radarr redacts it server-side via the `privacy:"password"` annotation; the
// wire payload either omits the field or carries a placeholder. Mirror of
// sonarr.downloadClientDTO.
type downloadClientDTO struct {
	ID             int                   `json:"id"`
	Name           string                `json:"name"`
	Implementation string                `json:"implementation"`
	Enable         bool                  `json:"enable"`
	Fields         []downloadClientField `json:"fields"`
}

// downloadClientField mirrors Radarr's field-array entries on /downloadclient.
// We pluck host, port, username, category from this by `name` rather than
// decoding into a typed struct because the download-client field set varies per
// implementation. Mirror of sonarr.downloadClientField.
type downloadClientField struct {
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

// DownloadClient is the trimmed, typed shape ListDownloadClients returns to the
// qbit-discover handler. Mirror of sonarr.DownloadClient.
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

// ListNotifications calls Radarr GET /api/v3/notification and returns the
// trimmed Notification slice. Fields are preserved verbatim for the
// match-by-url loop and for shape mirroring in CreateNotification.
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
// Notification. Unlike sonarr there is no v3/v4 alias folding — radarr's movie
// triggers are surfaced verbatim so the reconciler can detect drift on them.
func notificationFromDTO(d notificationDTO) Notification {
	return Notification{
		ID: d.ID, Name: d.Name, Implementation: d.Implementation,
		OnGrab: d.OnGrab, OnDownload: d.OnDownload,
		OnMovieAdded:                d.OnMovieAdded,
		OnMovieDelete:               d.OnMovieDelete,
		OnMovieFileDelete:           d.OnMovieFileDelete,
		OnRename:                    d.OnRename,
		OnManualInteractionRequired: d.OnManualInteractionRequired,
		OnHealthIssue:               d.OnHealthIssue,
		Fields:                      d.Fields,
	}
}

// CreateNotification POSTs a Webhook notification to Radarr and returns the
// created Notification. The payload mirrors any TemplateFields supplied by the
// caller; otherwise a minimal known-good template is used.
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

// TestNotification asks Radarr to exercise the Webhook notification end-to-end
// via POST /api/v3/notification/test. A 2xx means Radarr could reach seasonfill
// AND our auth was accepted. Any non-2xx or transport error is returned
// verbatim (mapped to *StatusError / ErrInstanceUnauthorized /
// ErrInstanceNetwork by the transport layer).
//
// The body mirrors CreateNotification exactly. The test endpoint returns no
// body we need, hence out=nil — decoding an empty response would yield a false
// EOF error, which is why this does NOT route through submitNotification.
//
// Unsupported-trigger fallback matches submitNotification: an older Radarr that
// rejects a newer trigger with HTTP 400 gets exactly one retry with the newer
// triggers dropped. All other errors propagate.
func (c *Client) TestNotification(ctx context.Context, p NotificationPayload) error {
	body := notificationDTO{
		Name:           p.Name,
		Implementation: "Webhook",
		ConfigContract: "WebhookSettings",
		Fields:         buildNotificationFields(p),
	}
	setDesiredTriggers(&body)
	const endpoint = "/api/v3/notification/test"
	err := c.post(ctx, endpoint, body, nil)
	if err == nil {
		return nil
	}
	if !isUnsupportedTriggerErr(err) {
		return err
	}
	c.logger.WarnContext(ctx, "radarr_notification_test_unsupported_triggers_fallback",
		slog.String("instance", string(c.name)),
		slog.String("error", err.Error()),
	)
	dropUnsupportedTriggers(&body)
	return c.post(ctx, endpoint, body, nil)
}

// UpdateNotification PUTs an existing Webhook notification by ID, rewriting
// url + headers while preserving any other field the caller carried in
// existing.Fields (version-variance defence). The full desired trigger set is
// re-applied so a notification created by an older seasonfill (fewer triggers)
// is upgraded in place. existing.ID is reused verbatim so Radarr matches the
// row.
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

// desiredTriggerDTO is the SINGLE source of the trigger set seasonfill wants on
// the Radarr webhook. Aligned (M-FIX-4b) to exactly what the radarr inbox+mapper
// consume:
//
//	onGrab, onDownload, onMovieAdded, onMovieDelete, onMovieFileDelete, onRename
//
// onManualInteractionRequired and onHealthIssue are DELIBERATELY OFF: the inbox
// mapper classifies them Unsupported (dropped at ingest), so firing them is a
// wasted round-trip. onRename IS on because the mapper upserts `rename`.
//
// Both the outbound write (setDesiredTriggers) and the drift-check target
// (DesiredTriggers) derive from this factory so they cannot diverge — this is
// what makes triggersConverged stable (no perpetual churn).
func desiredTriggerDTO() notificationDTO {
	return notificationDTO{
		OnGrab:            true,
		OnDownload:        true,
		OnMovieAdded:      true,
		OnMovieDelete:     true,
		OnMovieFileDelete: true,
		OnRename:          true,
	}
}

// setDesiredTriggers turns on exactly the triggers desiredTriggerDTO declares.
// Create and Update both call it so their outbound payloads cannot drift from
// the drift-check target.
func setDesiredTriggers(dto *notificationDTO) {
	d := desiredTriggerDTO()
	dto.OnGrab = d.OnGrab
	dto.OnDownload = d.OnDownload
	dto.OnMovieAdded = d.OnMovieAdded
	dto.OnMovieDelete = d.OnMovieDelete
	dto.OnMovieFileDelete = d.OnMovieFileDelete
	dto.OnRename = d.OnRename
	// Explicitly OFF: our writes clear any operator-set value so the persisted
	// resource matches DesiredTriggers() and triggersConverged stays stable.
	dto.OnManualInteractionRequired = false
	dto.OnHealthIssue = false
}

// DesiredTriggers is the readable trigger set seasonfill wants on the webhook,
// projected through the SAME notificationFromDTO a listed Notification goes
// through — so it is directly ==-comparable to Notification.Triggers().
func DesiredTriggers() TriggerSet {
	return notificationFromDTO(desiredTriggerDTO()).Triggers()
}

// dropUnsupportedTriggers strips the newer movie triggers an older Radarr may not
// recognise, leaving the known-good core (onGrab/onDownload) plus onRename (a
// long-standing trigger no Radarr rejects). Used by the version-variance fallback
// after a 400 whose body names one of the newer movie trigger fields. The dropped
// fields carry omitempty so they vanish from the retried payload.
func dropUnsupportedTriggers(dto *notificationDTO) {
	dto.OnMovieAdded = false
	dto.OnMovieDelete = false
	dto.OnMovieFileDelete = false
}

// submitNotification POSTs (isPut=false) or PUTs (isPut=true) the notification
// body, retrying once without the newer trigger fields when Radarr rejects them
// (isUnsupportedTriggerErr). All other errors propagate. Shared by Create and
// Update so the fallback logic lives in one place. Mirror of
// sonarr.submitNotification.
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
		c.logger.WarnContext(ctx, "radarr_notification_unsupported_triggers_fallback",
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

// DeleteNotification removes the Radarr webhook entry by ID. Used on instance
// delete to keep Radarr's notification list clean. Caller treats errors as
// best-effort (log + continue). Mirror of sonarr.DeleteNotification.
func (c *Client) DeleteNotification(ctx context.Context, id int) error {
	if id == 0 {
		return fmt.Errorf("delete notification: missing id")
	}
	return c.delete(ctx, "/api/v3/notification/"+strconv.Itoa(id))
}

// WebhookFieldURL extracts the raw URL string from a notification's fields
// array. Returns "" when absent or not a string. Mirror of
// sonarr.WebhookFieldURL.
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

// isUnsupportedTriggerErr returns true when Radarr rejected the write body
// specifically because one of the newer movie trigger fields (onMovieAdded /
// onMovieDelete / onMovieFileDelete) is unknown to it (older Radarr). Rule:
// HTTP 400 with body
// containing the trigger name (case-insensitive). All other failure modes —
// network, auth, 5xx, other 4xx — return false so they propagate. Mirror of
// sonarr.isUnsupportedTriggerErr.
func isUnsupportedTriggerErr(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	if se.Status != 400 {
		return false
	}
	body := strings.ToLower(se.Body)
	return strings.Contains(body, "onmovieadded") ||
		strings.Contains(body, "onmoviedelete") ||
		strings.Contains(body, "onmoviefiledelete")
}

// buildNotificationFields constructs the Radarr notification.fields array. If
// TemplateFields is supplied, url and headers are substituted in-place; every
// other entry is preserved verbatim. Otherwise a minimal known-good template is
// emitted. Radarr, like Sonarr, expects headers as an array of {key,value}
// objects (IEnumerable<KeyValuePair<string,string>>), not a plain string.
// Mirror of sonarr.buildNotificationFields.
func buildNotificationFields(p NotificationPayload) []NotificationField {
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

// ListDownloadClients calls Radarr GET /api/v3/downloadclient and returns the
// trimmed DownloadClient slice. The host/port/username/category lookup is
// best-effort: missing fields yield zero values. Mirror of
// sonarr.ListDownloadClients — the ONE divergence is the category field name:
// Radarr's qBittorrent client emits `movieCategory` where Sonarr emits
// `tvCategory` (newer builds of both emit the neutral `category`), so all
// applicable names are accepted.
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
			case "category", "movieCategory":
				if s, ok := f.Value.(string); ok {
					dc.Category = s
				}
			}
		}
		out = append(out, dc)
	}
	return out, nil
}

// toInt is a lenient JSON-number → int coercion. Radarr emits port as either a
// JSON number (float64 after decode) or an int-shaped string depending on field
// type; we tolerate both. Mirror of sonarr.toInt (the radarr package had no such
// helper before this story — no symbol collision).
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
