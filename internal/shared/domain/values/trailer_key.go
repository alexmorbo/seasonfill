package values

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// TrailerKey is a YouTube video key (the v= query param value).
// Canonical shape: 11 chars from [A-Za-z0-9_-]. Other trailer
// providers (Vimeo etc.) would need a separate VO.
type TrailerKey struct {
	value string
}

var trailerKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

func NewTrailerKey(s string) (TrailerKey, error) {
	if !trailerKeyPattern.MatchString(s) {
		return TrailerKey{}, fmt.Errorf("%w: got %q", ErrTrailerKeyInvalid, s)
	}
	return TrailerKey{value: s}, nil
}

func (t TrailerKey) Value() string           { return t.value }
func (t TrailerKey) IsZero() bool            { return t.value == "" }
func (t TrailerKey) Equal(o TrailerKey) bool { return t.value == o.value }
func (t TrailerKey) String() string          { return t.value }

func (t TrailerKey) MarshalJSON() ([]byte, error) {
	if t.value == "" {
		return []byte("null"), nil
	}
	return json.Marshal(t.value)
}

func (t *TrailerKey) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = TrailerKey{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("values: trailer_key unmarshal: %w", err)
	}
	parsed, err := NewTrailerKey(s)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}
