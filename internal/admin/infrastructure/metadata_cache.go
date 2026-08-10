// Package infrastructure hosts adapters for the admin bounded context.
// metadata_cache.go owns the two per-instance LRU caches that front the
// Sonarr /api/v3/qualityprofile and /api/v3/rootfolder endpoints (N-4b,
// Story 519). TTL 10 min matches the FE staleTime.
package infrastructure

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// MetadataCacheTTL is the default TTL for both metadata caches.
const MetadataCacheTTL = 10 * time.Minute

// MetadataCacheCapacity bounds each cache by entry count (≈100 instances).
const MetadataCacheCapacity = 100

// MetadataCache fronts the two per-instance Sonarr metadata reads. Keyed
// by instance name (the arr_instance PK) — ADR-0008 S1-C: the former
// int64 id key was always 0 (no ID column on the model), so every
// instance collided on key 0 and served the first instance's profiles.
// `nameSuffix` is appended to the underlying cachewatch names so tests
// can register multiple parallel instances without colliding on the
// singleton registry.
type MetadataCache struct {
	qualityProfiles *cachewatch.Cache[string, []ports.QualityProfile]
	rootFolders     *cachewatch.Cache[string, []ports.RootFolder]
}

// NewMetadataCache constructs a MetadataCache with the production TTL +
// capacity.
func NewMetadataCache(nameSuffix string) *MetadataCache {
	qpSizer := func(_ string, _ []ports.QualityProfile) int { return 1 }
	rfSizer := func(_ string, _ []ports.RootFolder) int { return 1 }
	return &MetadataCache{
		qualityProfiles: cachewatch.New[string, []ports.QualityProfile](
			"instance_metadata_quality_profiles"+nameSuffix,
			MetadataCacheCapacity, MetadataCacheTTL, qpSizer,
		),
		rootFolders: cachewatch.New[string, []ports.RootFolder](
			"instance_metadata_root_folders"+nameSuffix,
			MetadataCacheCapacity, MetadataCacheTTL, rfSizer,
		),
	}
}

func (c *MetadataCache) GetQualityProfiles(name string) ([]ports.QualityProfile, bool) {
	return c.qualityProfiles.Get(name)
}

func (c *MetadataCache) SetQualityProfiles(name string, items []ports.QualityProfile) {
	c.qualityProfiles.Add(name, items)
}

func (c *MetadataCache) GetRootFolders(name string) ([]ports.RootFolder, bool) {
	return c.rootFolders.Get(name)
}

func (c *MetadataCache) SetRootFolders(name string, items []ports.RootFolder) {
	c.rootFolders.Add(name, items)
}

// InvalidateInstance evicts both caches for the given instance name.
// Counted as reason="manual" — operator-driven refresh or PUT-instance
// reconfigure (Story 521 wires the second call site).
func (c *MetadataCache) InvalidateInstance(name string) {
	c.qualityProfiles.Remove(name)
	c.rootFolders.Remove(name)
}

// Close stops the underlying TTL reapers. Idempotent.
func (c *MetadataCache) Close() error {
	_ = c.qualityProfiles.Close()
	_ = c.rootFolders.Close()
	return nil
}
