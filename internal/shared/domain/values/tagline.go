package values

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Tagline struct {
	value string
	lang  LanguageTag
}

func NewTagline(value string, lang LanguageTag) (Tagline, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return Tagline{}, fmt.Errorf("%w", ErrTaglineEmpty)
	}
	if lang.IsZero() {
		return Tagline{}, fmt.Errorf("%w: empty lang tag", ErrTaglineEmpty)
	}
	return Tagline{value: v, lang: lang}, nil
}

func (t Tagline) Value() string        { return t.value }
func (t Tagline) Lang() LanguageTag    { return t.lang }
func (t Tagline) IsZero() bool         { return t.value == "" }
func (t Tagline) Equal(o Tagline) bool { return t.value == o.value && t.lang.Equal(o.lang) }
func (t Tagline) String() string       { return t.value }

type taglineJSON struct {
	Value string      `json:"value"`
	Lang  LanguageTag `json:"lang"`
}

func (t Tagline) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(taglineJSON{Value: t.value, Lang: t.lang})
}

func (t *Tagline) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = Tagline{}
		return nil
	}
	var raw taglineJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("values: tagline unmarshal: %w", err)
	}
	parsed, err := NewTagline(raw.Value, raw.Lang)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}
