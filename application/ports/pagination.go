package ports

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidCursor = errors.New("invalid cursor")
	ErrInvalidLimit  = errors.New("invalid limit")
)

// MaxListLimit is the hard upper bound on Pagination.Limit at the port edge.
// HTTP callers should clamp to a tighter default (50) and max (200).
const MaxListLimit = 1000

// Cursor is an opaque keyset position over (created_at, id). Encoded as
// base64url(JSON({"ts":"<RFC3339Nano>","id":"<uuid>"})). Nil / empty
// means "first page".
type Cursor struct {
	Timestamp time.Time
	ID        string
}

type cursorPayload struct {
	TS string `json:"ts"`
	ID string `json:"id"`
}

func (c *Cursor) String() string {
	if c == nil {
		return ""
	}
	raw, err := json.Marshal(cursorPayload{
		TS: c.Timestamp.UTC().Format(time.RFC3339Nano),
		ID: c.ID,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ParseCursor decodes a Cursor.String() result. The empty string parses
// to (nil, nil). Any failure returns ErrInvalidCursor wrapped with context.
func ParseCursor(s string) (*Cursor, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: base64: %w", ErrInvalidCursor, err)
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: json: %w", ErrInvalidCursor, err)
	}
	if p.TS == "" || p.ID == "" {
		return nil, fmt.Errorf("%w: missing fields", ErrInvalidCursor)
	}
	ts, err := time.Parse(time.RFC3339Nano, p.TS)
	if err != nil {
		return nil, fmt.Errorf("%w: timestamp: %w", ErrInvalidCursor, err)
	}
	return &Cursor{Timestamp: ts.UTC(), ID: p.ID}, nil
}

// Pagination is the page-window selector for every audit repository List.
// A nil Cursor means "first page".
type Pagination struct {
	Limit  int
	Cursor *Cursor
}
