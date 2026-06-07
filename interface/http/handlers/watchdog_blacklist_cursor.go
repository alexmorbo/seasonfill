package handlers

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// encodeBlacklistCursor packs (created_at, id) into an opaque base64
// string. Format: base64("<unixNano>:<id>"). Matches the keyset
// predicate in WatchdogBlacklistRepository.ListByInstanceWithLimit.
func encodeBlacklistCursor(at time.Time, id uint) string {
	raw := strconv.FormatInt(at.UnixNano(), 10) + ":" + strconv.FormatUint(uint64(id), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeBlacklistCursor reverses encodeBlacklistCursor.
func decodeBlacklistCursor(s string) (time.Time, uint, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, err
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, errors.New("invalid cursor payload")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, 0, err
	}
	id, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, err
	}
	return time.Unix(0, ns).UTC(), uint(id), nil
}
