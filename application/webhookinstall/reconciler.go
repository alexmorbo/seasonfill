package webhookinstall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

// ErrUnknownInstance is returned when InstanceLookup has no entry for
// the supplied name. Caller decides whether to surface — instance
// Create/Update synchronous reconciles ignore (the registry may not
// yet reflect the just-written row); GET /webhook/status maps to 404.
var ErrUnknownInstance = errors.New("webhookinstall: unknown instance")

// DefaultRetryAfter is how long the reconciler asks the background
// loop (041d) to wait before the next attempt after a failure. The
// same value gates lazy refresh from GET /webhook/status — a Status
// with LastError is stale until now >= NextRetryAt.
const DefaultRetryAfter = 5 * time.Minute

// DefaultRefreshAfter is the cache TTL for successful Status entries.
// 15 min picked deliberately small so an operator fixing a wrong
// override sees the badge flip on the next dashboard open without
// waiting for the 5-min background tick.
const DefaultRefreshAfter = 15 * time.Minute

// Reconciler ensures one Sonarr instance carries the seasonfill
// webhook at the expected URL. Idempotent: two consecutive calls with
// no external change update the cache only. Concurrent calls for the
// same instance race on Sonarr writes but never corrupt the cache —
// every attempt fully replaces the entry.
type Reconciler struct {
	lookup    InstanceLookup
	publicURL PublicURLFunc
	cache     *StatusCache
	apiKey    string
	logger    *slog.Logger
	now       func() time.Time
}

type Deps struct {
	Lookup    InstanceLookup
	PublicURL PublicURLFunc
	Cache     *StatusCache
	APIKey    string
	Logger    *slog.Logger
}

func New(d Deps) *Reconciler {
	if d.Cache == nil {
		panic("webhookinstall.New: nil StatusCache")
	}
	if d.Lookup == nil {
		panic("webhookinstall.New: nil InstanceLookup")
	}
	lg := d.Logger
	if lg == nil {
		lg = slog.Default()
	}
	pf := d.PublicURL
	if pf == nil {
		pf = func(context.Context) string { return "" }
	}
	return &Reconciler{
		lookup: d.Lookup, publicURL: pf, cache: d.Cache,
		apiKey: d.APIKey, logger: lg, now: time.Now,
	}
}

// WithClock — tests only.
func (r *Reconciler) WithClock(f func() time.Time) *Reconciler { r.now = f; return r }

// Reconcile ensures the Sonarr-side webhook matches the expected URL.
// Behaviour:
//   - WebhookInstallEnabled=false → cache {Installed:false, no error},
//     no Sonarr call.
//   - PublicURL unresolved → cache LastError "public_url undetermined"
//   - NextRetryAt; returns (status, error).
//   - Existing webhook with matching URL → cache Installed=true, no
//     write.
//   - Existing webhook with different URL → UpdateNotification, cache
//     reflects new URL.
//   - No existing webhook → CreateNotification.
//   - Sonarr error at any step → cache LastError + NextRetryAt, return.
//
// NEVER panics. NEVER deletes cache entries — caller uses
// StatusCache.Delete via HandleInstanceDeleted.
func (r *Reconciler) Reconcile(ctx context.Context, instanceName string) (Status, error) {
	snap, notifier, ok := r.lookup(instanceName)
	if !ok {
		return Status{}, fmt.Errorf("%w: %s", ErrUnknownInstance, instanceName)
	}
	now := r.now()

	if !snap.WebhookInstallEnabled {
		st := Status{Installed: false, LastCheckedAt: now}
		r.cache.Set(instanceName, st)
		return st, nil
	}

	publicURL := snap.WebhookBaseURL(r.publicURL(ctx))
	if publicURL == "" {
		msg := "public_url undetermined"
		next := now.Add(DefaultRetryAfter)
		st := Status{LastError: &msg, LastCheckedAt: now, NextRetryAt: &next}
		r.cache.Set(instanceName, st)
		return st, errors.New(msg)
	}

	expectedURL := publicURL + CanonicalPath(instanceName)

	existing, err := notifier.ListNotifications(ctx)
	if err != nil {
		return r.recordFailure(ctx, instanceName, now, "list_notifications", err), err
	}

	var match *sonarr.Notification
	for i := range existing {
		n := &existing[i]
		if n.Implementation != "Webhook" {
			continue
		}
		if MatchesWebhookURL(n.Fields, instanceName) {
			match = n
			break
		}
	}

	switch {
	case match == nil:
		created, err := notifier.CreateNotification(ctx, sonarr.NotificationPayload{
			Name:           "seasonfill",
			URL:            expectedURL,
			APIKeyHeader:   r.apiKey,
			TemplateFields: pickTemplateFields(existing),
		})
		if err != nil {
			return r.recordFailure(ctx, instanceName, now, "create_notification", err), err
		}
		st := r.successStatus(now, created.ID, expectedURL)
		r.cache.Set(instanceName, st)
		r.logger.InfoContext(ctx, "webhook_reconciled",
			slog.String("instance", instanceName),
			slog.String("action", "create"),
			slog.Int("notification_id", created.ID))
		return st, nil

	case sonarr.WebhookFieldURL(match.Fields) == expectedURL:
		st := r.successStatus(now, match.ID, expectedURL)
		r.cache.Set(instanceName, st)
		return st, nil

	default:
		updated, err := notifier.UpdateNotification(ctx, *match, sonarr.NotificationPayload{
			Name: "seasonfill", URL: expectedURL, APIKeyHeader: r.apiKey,
		})
		if err != nil {
			return r.recordFailure(ctx, instanceName, now, "update_notification", err), err
		}
		st := r.successStatus(now, updated.ID, expectedURL)
		r.cache.Set(instanceName, st)
		r.logger.InfoContext(ctx, "webhook_reconciled",
			slog.String("instance", instanceName),
			slog.String("action", "update"),
			slog.Int("notification_id", updated.ID),
			slog.String("url", expectedURL))
		return st, nil
	}
}

// GetStatus returns the cached Status, lazily triggering Reconcile
// when the entry is missing, stale (older than DefaultRefreshAfter),
// or carries a LastError past NextRetryAt. A lazy-reconcile failure
// leaves the previous Status on the wire so the dashboard can still
// render the error.
func (r *Reconciler) GetStatus(ctx context.Context, instanceName string) (Status, error) {
	cur, hit := r.cache.Get(instanceName)
	now := r.now()
	if hit && !r.shouldRefresh(cur, now) {
		return cur, nil
	}
	return r.Reconcile(ctx, instanceName)
}

// HandleInstanceDeleted is the cleanup hook fired after a successful
// row delete. Best-effort: Sonarr unreachable or already-gone is a
// WARN, not an error. Cache entry is purged unconditionally so a
// re-created instance starts fresh.
func (r *Reconciler) HandleInstanceDeleted(ctx context.Context, instanceName string) {
	defer r.cache.Delete(instanceName)
	cur, hit := r.cache.Get(instanceName)
	if !hit || !cur.Installed || cur.NotificationID == nil {
		return
	}
	_, notifier, ok := r.lookup(instanceName)
	if !ok {
		r.logger.WarnContext(ctx, "webhook_cleanup_skipped_unknown_instance",
			slog.String("instance", instanceName))
		return
	}
	if err := notifier.DeleteNotification(ctx, *cur.NotificationID); err != nil {
		r.logger.WarnContext(ctx, "webhook_cleanup_failed",
			slog.String("instance", instanceName),
			slog.Int("notification_id", *cur.NotificationID),
			slog.String("error", err.Error()))
		return
	}
	r.logger.InfoContext(ctx, "webhook_cleanup_ok",
		slog.String("instance", instanceName),
		slog.Int("notification_id", *cur.NotificationID))
}

func (r *Reconciler) shouldRefresh(s Status, now time.Time) bool {
	if s.LastError != nil {
		return s.NextRetryAt == nil || !now.Before(*s.NextRetryAt)
	}
	if s.LastCheckedAt.IsZero() {
		return true
	}
	return now.Sub(s.LastCheckedAt) >= DefaultRefreshAfter
}

func (r *Reconciler) successStatus(now time.Time, id int, installedURL string) Status {
	idCopy := id
	urlCopy := installedURL
	return Status{
		Installed:      true,
		NotificationID: &idCopy,
		InstalledURL:   &urlCopy,
		LastCheckedAt:  now,
	}
}

// recordFailure preserves the previous Installed+NotificationID so
// the dashboard can still show "currently installed but last check
// failed". 041d uses LastError to gate retries.
func (r *Reconciler) recordFailure(ctx context.Context, instanceName string, now time.Time, op string, err error) Status {
	prev, _ := r.cache.Get(instanceName)
	msg := op + ": " + err.Error()
	if errors.Is(err, domain.ErrInstanceUnauthorized) {
		msg = op + ": sonarr unauthorized"
	}
	next := now.Add(DefaultRetryAfter)
	out := Status{
		Installed:      prev.Installed,
		NotificationID: prev.NotificationID,
		InstalledURL:   prev.InstalledURL,
		LastError:      &msg,
		LastCheckedAt:  now,
		NextRetryAt:    &next,
	}
	r.cache.Set(instanceName, out)
	r.logger.WarnContext(ctx, "webhook_reconcile_failed",
		slog.String("instance", instanceName),
		slog.String("op", op),
		slog.String("error", err.Error()))
	return out
}

// pickTemplateFields returns the field array of the first existing
// Webhook notification — defends against Sonarr field-schema drift
// across versions. Mirrors the helper that used to live in
// interface/http/handlers/webhook_install.go.
func pickTemplateFields(list []sonarr.Notification) []sonarr.NotificationField {
	for _, n := range list {
		if n.Implementation == "Webhook" && len(n.Fields) > 0 {
			return n.Fields
		}
	}
	return nil
}
