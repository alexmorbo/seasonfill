// Package app — tag_resolver.go ships the N-4c TagResolver: maps a
// (user, instance) tuple to an arr tag.id. Cache hits in
// user_instance_tags skip the arr entirely; cache miss falls through to
// ListTags + (if absent) CreateTag, then writes the cache row.
//
// Arr-neutral since R-6: the same resolver serves the Sonarr series-add
// and the Radarr movie-add. arr_instance.name is unique across both arr
// kinds (`type` discriminator), so the (user_id, instance_name) cache
// key already pins which arr a row belongs to — no per-arr columns and
// no kind parameter are needed.
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

// ArrTagPort is the narrow per-instance arr surface the resolver reads:
// list existing tags + create on miss. Satisfied by ports.SonarrClient
// AND ports.RadarrClient (both expose ListTags + CreateTag), so the
// wiring layer can pass a runtime client straight through.
type ArrTagPort interface {
	ListTags(ctx context.Context) ([]ports.Tag, error)
	CreateTag(ctx context.Context, label string) (ports.Tag, error)
}

// TagCachePort persists the (userID, instanceName) → (tagID, label)
// mapping. Satisfied by *adminpersistence.UserInstanceTagRepository.
type TagCachePort interface {
	Get(ctx context.Context, userID uint, instanceName domain.InstanceName) (admin.UserInstanceTag, error)
	Upsert(ctx context.Context, t admin.UserInstanceTag) error
}

// TagResolver maps (user, instance) → arr tag.id with a write-through
// cache. The cache key is (userID, instanceName); bypass mode
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

// Resolve returns the arr tag.id for (user, instance). On cache hit no
// arr call is issued. On miss the resolver calls ListTags + (if absent)
// CreateTag, then writes the cache row. Upsert errors are logged but do
// not fail the call — the next call retries the cache path.
func (r *TagResolver) Resolve(
	ctx context.Context,
	arr ArrTagPort,
	user *admin.User,
	instanceName domain.InstanceName,
) (int, string, error) {
	label := UserTagLabel(user)
	var userID uint
	if user != nil {
		userID = user.ID
	}

	cached, err := r.cache.Get(ctx, userID, instanceName)
	if err == nil && cached.ArrTagID > 0 {
		return cached.ArrTagID, cached.ArrTagLabel, nil
	}
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		// Transient cache read failure — log and fall through.
		r.log.WarnContext(ctx, "tag_cache_get_failed",
			slog.Uint64("user_id", uint64(userID)),
			slog.String("instance", string(instanceName)),
			slog.String("error", err.Error()))
	}

	tags, err := arr.ListTags(ctx)
	if err != nil {
		return 0, label, fmt.Errorf("list tags: %w", err)
	}
	for _, t := range tags {
		if t.Label == label {
			r.writeCache(ctx, userID, instanceName, t.ID, label)
			return t.ID, label, nil
		}
	}

	created, err := arr.CreateTag(ctx, label)
	if err != nil {
		return 0, label, fmt.Errorf("create tag: %w", err)
	}
	r.writeCache(ctx, userID, instanceName, created.ID, label)
	return created.ID, label, nil
}

func (r *TagResolver) writeCache(ctx context.Context, userID uint, name domain.InstanceName, tagID int, label string) {
	if err := r.cache.Upsert(ctx, admin.UserInstanceTag{
		UserID:       userID,
		InstanceName: name,
		ArrTagID:     tagID,
		ArrTagLabel:  label,
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
