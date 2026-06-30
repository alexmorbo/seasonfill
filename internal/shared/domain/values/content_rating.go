package values

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ContentRating is the TV/MPAA content rating tag. Allowed values cover
// both US TV (TV-Y..TV-MA) and US movie (G..NC-17) rating systems plus
// the NR "not rated" sentinel. Future expansion (e.g. EU ratings) gets
// added to allowedContentRatings — the constructor is the single
// validation point.
type ContentRating struct {
	value string
}

var allowedContentRatings = map[string]struct{}{
	"TV-Y":  {},
	"TV-Y7": {},
	"TV-G":  {},
	"TV-PG": {},
	"TV-14": {},
	"TV-MA": {},
	"G":     {},
	"PG":    {},
	"PG-13": {},
	"R":     {},
	"NC-17": {},
	"NR":    {},
}

func NewContentRating(s string) (ContentRating, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if _, ok := allowedContentRatings[s]; !ok {
		return ContentRating{}, fmt.Errorf("%w: got %q", ErrContentRatingInvalid, s)
	}
	return ContentRating{value: s}, nil
}

func (c ContentRating) Value() string              { return c.value }
func (c ContentRating) IsZero() bool               { return c.value == "" }
func (c ContentRating) Equal(o ContentRating) bool { return c.value == o.value }
func (c ContentRating) String() string             { return c.value }

func (c ContentRating) MarshalJSON() ([]byte, error) {
	if c.value == "" {
		return []byte("null"), nil
	}
	return json.Marshal(c.value)
}

func (c *ContentRating) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*c = ContentRating{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("values: content_rating unmarshal: %w", err)
	}
	parsed, err := NewContentRating(s)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}
