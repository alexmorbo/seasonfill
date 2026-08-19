package domain

import (
	"fmt"

	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MediaID is the value object identifying one media item to the engine: the
// vertical it belongs to (MediaType), our internal surrogate primary key (the
// movies/series canon row PK), and its TMDB id (the sole source of truth for
// metadata — see project_seasonfill_tmdb_sole_truth). The pair (Type, internal
// id) is unique; TMDBID is carried for plugin adapters that fetch from TMDB.
//
// MediaID is comparable and immutable; construct it via NewMediaID. The zero
// value is invalid (Valid() == false).
type MediaID struct {
	mediaType  MediaType
	internalID int64
	tmdbID     shareddomain.TMDBID
}

// NewMediaID validates and constructs a MediaID. mediaType must be a known
// MediaType and internalID must be positive (our surrogate PKs are 1-based).
// tmdbID may be the zero sentinel (unknown) — validation does not require it,
// because a stub row can exist before its TMDB id is resolved.
func NewMediaID(mediaType MediaType, internalID int64, tmdbID shareddomain.TMDBID) (MediaID, error) {
	if !mediaType.Valid() {
		return MediaID{}, fmt.Errorf("%w: media type %q", ErrInvalidMediaID, mediaType.String())
	}
	if internalID <= 0 {
		return MediaID{}, fmt.Errorf("%w: internal id %d", ErrInvalidMediaID, internalID)
	}
	return MediaID{mediaType: mediaType, internalID: internalID, tmdbID: tmdbID}, nil
}

// Type returns the media vertical. The engine uses this to select the plugin
// set via SectionRegistry.For(Type()).
func (m MediaID) Type() MediaType { return m.mediaType }

// InternalID returns our surrogate primary key.
func (m MediaID) InternalID() int64 { return m.internalID }

// TMDBID returns the TMDB id (may be the zero sentinel).
func (m MediaID) TMDBID() shareddomain.TMDBID { return m.tmdbID }

// Valid reports whether m is a well-formed identifier.
func (m MediaID) Valid() bool {
	return m.mediaType.Valid() && m.internalID > 0
}

// Key returns a stable string identity for the (type, internal id) pair, used
// as the singleflight coalescing key prefix in the Freshener (e.g. "movie-123").
func (m MediaID) Key() string {
	return fmt.Sprintf("%s-%d", m.mediaType.String(), m.internalID)
}
