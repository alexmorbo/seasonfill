// Package domain holds shared cross-bounded-context domain primitives.
// ids.go defines typed aliases for every ID space in the system — primitive
// obsession defense. Compiler refuses to mix SonarrSeriesID with SeriesID
// even though both are int64 underneath; PRD §6.3.1 Level 2.
package domain

import (
	"errors"
	"regexp"
	"strings"
)

// Internal IDs — primary keys in our own database.
type (
	SeriesID   int64
	UserID     int64
	InstanceID int64
	GrabID     int64
	EpisodeID  int64
)

// Sonarr external IDs — Sonarr's own integer IDs, NOT our internal ones.
type (
	SonarrSeriesID  int
	SonarrEpisodeID int
	SonarrTagID     int
)

// Radarr external IDs — reserved for the future Movies iteration (§5.1.4).
// Declared now so cross-context code can reference them without a follow-up
// API break when Radarr support lands.
type (
	RadarrMovieID int
	RadarrTagID   int
)

// External catalog source IDs — canonical source-of-truth from external
// providers. TMDB/TVDB are integers; IMDB is a "tt"-prefixed string.
type (
	TMDBID int
	TVDBID int
	IMDBID string
)

// Transport identifiers. QbitHash is qBittorrent's torrent hash —
// lowercase 40-char hex; constructor enforces normalization.
type QbitHash string

// Sentinel errors returned by the constructors below.
var (
	ErrInvalidIMDBID   = errors.New("imdb id must match ^tt\\d+$")
	ErrInvalidQbitHash = errors.New("qbit hash must be lowercase 40-char hex")
)

var (
	imdbIDPattern   = regexp.MustCompile(`^tt\d+$`)
	qbitHashPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// NewIMDBID validates and constructs an IMDBID. Whitespace is trimmed.
// Case-sensitive: "TT0000001" is rejected (IMDB ids are canonically lower-tt).
func NewIMDBID(s string) (IMDBID, error) {
	s = strings.TrimSpace(s)
	if !imdbIDPattern.MatchString(s) {
		return "", ErrInvalidIMDBID
	}
	return IMDBID(s), nil
}

// NewQbitHash validates, trims and lowercases a qBittorrent torrent hash.
// Uppercase hex is accepted on input and normalized to lowercase on output —
// qBittorrent's HTTP API is case-insensitive but emits lowercase, so we
// canonicalize at the boundary.
func NewQbitHash(s string) (QbitHash, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !qbitHashPattern.MatchString(s) {
		return "", ErrInvalidQbitHash
	}
	return QbitHash(s), nil
}
