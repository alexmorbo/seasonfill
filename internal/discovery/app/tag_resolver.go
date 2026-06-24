// Package app — tag_resolver.go ships the N-4c TagResolver: maps a
// (user, instance) tuple to a Sonarr tag.id. Cache hits in
// user_instance_tags skip Sonarr entirely; cache miss falls through to
// ListTags + (if absent) CreateTag, then writes the cache row.
//
// NormalizeUsername produces the "sf-<slug>" label per PRD §5.3.1:
// lowercase, non-alphanumeric → "-", dedupe + trim, length cap 30
// after the "sf-" prefix (≤33 total). Bypass mode (user==nil) yields
// "sf-system".
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SonarrTagPort is the narrow per-instance Sonarr surface the resolver
// reads: list existing tags + create on miss. The wiring layer adapts
// a runtime *sonarr.Client to this two-method port via a closure.
type SonarrTagPort interface {
	ListTags(ctx context.Context) ([]ports.Tag, error)
	CreateTag(ctx context.Context, label string) (ports.Tag, error)
}

// TagCachePort persists the (userID, instanceName) → (tagID, label)
// mapping. Satisfied by *adminpersistence.UserInstanceTagRepository.
type TagCachePort interface {
	Get(ctx context.Context, userID uint, instanceName domain.InstanceName) (admin.UserInstanceTag, error)
	Upsert(ctx context.Context, t admin.UserInstanceTag) error
}

// TagResolver maps (user, instance) → Sonarr tag.id with a write-
// through cache. The cache key is (userID, instanceName); bypass mode
// (user==nil) uses userID=0 — the unique key is then (0, instanceName)
// which collapses every bypass caller onto a shared "sf-system" tag.
type TagResolver struct {
	cache TagCachePort
	log   *slog.Logger
}

// NewTagResolver panics on nil deps — init-time bug.
func NewTagResolver(cache TagCachePort, log *slog.Logger) *TagResolver {
	if cache == nil {
		panic("NewTagResolver: cache required")
	}
	if log == nil {
		panic("NewTagResolver: log required")
	}
	return &TagResolver{cache: cache, log: log}
}

// Resolve returns the Sonarr tag.id for (user, instance). On cache hit
// no Sonarr call is issued. On miss the resolver calls ListTags +
// (if absent) CreateTag, then writes the cache row. Upsert errors are
// logged but do not fail the call — the next call retries the cache
// path.
func (r *TagResolver) Resolve(
	ctx context.Context,
	sonarr SonarrTagPort,
	user *admin.User,
	instanceName domain.InstanceName,
) (int, string, error) {
	label := UserTagLabel(user)
	var userID uint
	if user != nil {
		userID = user.ID
	}

	cached, err := r.cache.Get(ctx, userID, instanceName)
	if err == nil && cached.SonarrTagID > 0 {
		return cached.SonarrTagID, cached.SonarrTagLabel, nil
	}
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		// Transient cache read failure — log and fall through.
		r.log.WarnContext(ctx, "tag_cache_get_failed",
			slog.Uint64("user_id", uint64(userID)),
			slog.String("instance", string(instanceName)),
			slog.String("error", err.Error()))
	}

	tags, err := sonarr.ListTags(ctx)
	if err != nil {
		return 0, label, fmt.Errorf("list tags: %w", err)
	}
	for _, t := range tags {
		if t.Label == label {
			r.writeCache(ctx, userID, instanceName, t.ID, label)
			return t.ID, label, nil
		}
	}

	created, err := sonarr.CreateTag(ctx, label)
	if err != nil {
		return 0, label, fmt.Errorf("create tag: %w", err)
	}
	r.writeCache(ctx, userID, instanceName, created.ID, label)
	return created.ID, label, nil
}

func (r *TagResolver) writeCache(ctx context.Context, userID uint, name domain.InstanceName, tagID int, label string) {
	if err := r.cache.Upsert(ctx, admin.UserInstanceTag{
		UserID:         userID,
		InstanceName:   name,
		SonarrTagID:    tagID,
		SonarrTagLabel: label,
	}); err != nil {
		r.log.WarnContext(ctx, "tag_cache_upsert_failed",
			slog.Uint64("user_id", uint64(userID)),
			slog.String("instance", string(name)),
			slog.String("error", err.Error()))
	}
}

// UserTagLabel returns "sf-<slug>" for a user, or "sf-system" for bypass.
func UserTagLabel(user *admin.User) string {
	if user == nil {
		return "sf-system"
	}
	return "sf-" + NormalizeUsername(user.Username)
}

// NormalizeUsername converts a freeform username into a slug suitable
// for a Sonarr tag label: lowercase, non-alphanumeric runs collapse to
// a single "-", leading/trailing "-" trimmed, capped to 30 chars (so
// the "sf-" prefix stays under the Sonarr 32-char limit by a safety
// margin). Empty result falls back to "user".
func NormalizeUsername(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevDash := false
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 30 {
		s = strings.TrimRight(s[:30], "-")
	}
	if s == "" {
		s = "user"
	}
	return s
}
