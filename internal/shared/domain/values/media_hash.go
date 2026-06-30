package values

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// MediaHash is a sha256 hex digest used as the content-addressed key
// for poster/backdrop/logo assets in the media bounded context. Must
// be exactly 64 chars, lowercase hex. The constructor does NOT
// normalize uppercase — incoming uppercase indicates a producer bug
// (everything seasonfill emits is lower-hex by convention).
type MediaHash struct {
	value string
}

var mediaHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func NewMediaHash(s string) (MediaHash, error) {
	if !mediaHashPattern.MatchString(s) {
		return MediaHash{}, fmt.Errorf("%w: got %q", ErrMediaHashInvalid, s)
	}
	return MediaHash{value: s}, nil
}

func (m MediaHash) Value() string          { return m.value }
func (m MediaHash) IsZero() bool           { return m.value == "" }
func (m MediaHash) Equal(o MediaHash) bool { return m.value == o.value }
func (m MediaHash) String() string         { return m.value }

func (m MediaHash) MarshalJSON() ([]byte, error) {
	if m.value == "" {
		return []byte("null"), nil
	}
	return json.Marshal(m.value)
}

func (m *MediaHash) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*m = MediaHash{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("values: media_hash unmarshal: %w", err)
	}
	parsed, err := NewMediaHash(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
