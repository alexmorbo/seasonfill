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
	SeriesID  int64
	UserID    int64
	GrabID    int64
	EpisodeID int64
)

// InstanceName is the config slug ("main", "anime", "kids") of a
// Sonarr/Radarr instance. Today this is the only identifier used in
// code — instances live in env-var/HCL config, not in the DB — so
// every "which instance?" parameter or field carries InstanceName,
// not InstanceID.
//
// Underlying kind is string; GORM persists it to instance_name
// VARCHAR/TEXT columns transparently. JSON marshals as a plain string.
type InstanceName string

// InstanceID is the internal BIGINT primary key reserved for a future
// runtime-config refactor where instances become first-class DB-backed
// objects (similar to how SeriesID/SonarrSeriesID split internal vs
// external series identification). NOT currently consumed by any
// callsite — see decisions/d622-instance-name-typing.md for the option-B
// design call. If/when instances become DB rows, InstanceName remains
// the user-facing slug (FK column) and InstanceID becomes the surrogate
// PK.
type InstanceID int64

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
