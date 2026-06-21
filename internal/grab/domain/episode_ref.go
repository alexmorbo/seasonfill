package grab

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// EpisodeRef pins one episode to one grab_records row. Populated by the
// OnGrab webhook from the Sonarr payload's `episodes[]` array.
// EpisodeNumber is the in-season ordinal (1..N), distinct from
// EpisodeID which is the Sonarr-side surrogate ID. GrabID is the
// uuid string of the parent grab_records row.
//
// 467a: the (grab_id, episode_id) composite PK on the episode_grabs
// table makes BatchUpsert idempotent — re-delivering the same Sonarr
// webhook updates updated_at without inserting duplicates.
type EpisodeRef struct {
	GrabID        string
	EpisodeID     domain.EpisodeID
	EpisodeNumber int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
