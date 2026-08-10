// blocklist.go declares the ADR-0017 Ф5 S3 discovery blocklist domain
// types. Global (pre-RBAC) hide-list for the discovery surface. Kind is
// an app-owned enum {tmdb, keyword}; ref_id is a tmdb_id (kind=tmdb) or a
// TMDB keyword_id (kind=keyword).
package domain

// BlocklistKind is the discriminator for a blocklist entry.
type BlocklistKind string

const (
	// BlocklistKindTMDB subtracts a series by its tmdb_id from every
	// discovery reader's output.
	BlocklistKindTMDB BlocklistKind = "tmdb"
	// BlocklistKindKeyword records a TMDB keyword_id to be excluded via
	// with_without_keywords on the discover passthrough (folded in by the
	// BE-keyword sibling story). Stored here so the two writers share one
	// table + cache.
	BlocklistKindKeyword BlocklistKind = "keyword"
)

// IsValid reports whether k is a known blocklist kind.
func (k BlocklistKind) IsValid() bool {
	switch k {
	case BlocklistKindTMDB, BlocklistKindKeyword:
		return true
	default:
		return false
	}
}

// BlocklistEntry is one persisted blocklist row.
//
// Label is the keyword name for keyword rows; nil for tmdb rows (the
// display title resolves from series_texts on read). RefID is a tmdb_id
// (kind=tmdb) or a TMDB keyword_id (kind=keyword) — int64 because TMDB
// keyword ids exceed int32.
type BlocklistEntry struct {
	ID    int64
	Kind  BlocklistKind
	RefID int64
	Label *string
}
