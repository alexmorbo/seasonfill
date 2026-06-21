package rest

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// encodeBlacklistCursor packs (blacklisted_at, series_id, season) into
// an opaque base64 string. Format: base64("<unixNano>:<series>:<season>").
// Matches the keyset predicate in
// WatchdogBlacklistRepository.ListByInstanceWithLimit.
func encodeBlacklistCursor(at time.Time, seriesID domain.SonarrSeriesID, season int) string {
	raw := strconv.FormatInt(at.UnixNano(), 10) + ":" +
		strconv.FormatInt(int64(seriesID), 10) + ":" +
		strconv.Itoa(season)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeBlacklistCursor reverses encodeBlacklistCursor.
func decodeBlacklistCursor(s string) (time.Time, domain.SonarrSeriesID, int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, 0, err
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 3 {
		return time.Time{}, 0, 0, errors.New("invalid cursor payload")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, 0, 0, err
	}
	sid, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, 0, err
	}
	season, err := strconv.Atoi(parts[2])
	if err != nil {
		return time.Time{}, 0, 0, err
	}
	return time.Unix(0, ns).UTC(), domain.SonarrSeriesID(sid), season, nil
}
