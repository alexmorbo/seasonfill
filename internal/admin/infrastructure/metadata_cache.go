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

// MetadataCache fronts the two per-instance Sonarr metadata reads.
// Keyed by instance.ID (int64) so renames don't invalidate. `nameSuffix`
// is appended to the underlying cachewatch names so tests can register
// multiple parallel instances without colliding on the singleton registry.
type MetadataCache struct {
	qualityProfiles *cachewatch.Cache[int64, []ports.QualityProfile]
	rootFolders     *cachewatch.Cache[int64, []ports.RootFolder]
}

// NewMetadataCache constructs a MetadataCache with the production TTL +
// capacity.
func NewMetadataCache(nameSuffix string) *MetadataCache {
	qpSizer := func(_ int64, _ []ports.QualityProfile) int { return 1 }
	rfSizer := func(_ int64, _ []ports.RootFolder) int { return 1 }
	return &MetadataCache{
		qualityProfiles: cachewatch.New[int64, []ports.QualityProfile](
			"instance_metadata_quality_profiles"+nameSuffix,
			MetadataCacheCapacity, MetadataCacheTTL, qpSizer,
		),
		rootFolders: cachewatch.New[int64, []ports.RootFolder](
			"instance_metadata_root_folders"+nameSuffix,
			MetadataCacheCapacity, MetadataCacheTTL, rfSizer,
		),
	}
}

func (c *MetadataCache) GetQualityProfiles(id int64) ([]ports.QualityProfile, bool) {
	return c.qualityProfiles.Get(id)
}

func (c *MetadataCache) SetQualityProfiles(id int64, items []ports.QualityProfile) {
	c.qualityProfiles.Add(id, items)
}

func (c *MetadataCache) GetRootFolders(id int64) ([]ports.RootFolder, bool) {
	return c.rootFolders.Get(id)
}

func (c *MetadataCache) SetRootFolders(id int64, items []ports.RootFolder) {
	c.rootFolders.Add(id, items)
}

// InvalidateInstance evicts both caches for the given instance id.
// Counted as reason="manual" — operator-driven refresh or PUT-instance
// reconfigure (Story 521 wires the second call site).
func (c *MetadataCache) InvalidateInstance(id int64) {
	c.qualityProfiles.Remove(id)
	c.rootFolders.Remove(id)
}

// Close stops the underlying TTL reapers. Idempotent.
func (c *MetadataCache) Close() error {
	_ = c.qualityProfiles.Close()
	_ = c.rootFolders.Close()
	return nil
}
