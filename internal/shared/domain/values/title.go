package values

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Title is a localized non-empty string. The lang tag travels with the
// value so downstream consumers never have to guess which language a
// title was rendered in.
type Title struct {
	value string
	lang  LanguageTag
}

func NewTitle(value string, lang LanguageTag) (Title, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return Title{}, fmt.Errorf("%w", ErrTitleEmpty)
	}
	if lang.IsZero() {
		return Title{}, fmt.Errorf("%w: empty lang tag", ErrTitleEmpty)
	}
	return Title{value: v, lang: lang}, nil
}

func (t Title) Value() string      { return t.value }
func (t Title) Lang() LanguageTag  { return t.lang }
func (t Title) IsZero() bool       { return t.value == "" }
func (t Title) Equal(o Title) bool { return t.value == o.value && t.lang.Equal(o.lang) }
func (t Title) String() string     { return t.value }

// titleJSON is the on-wire shape. We emit lang so multi-lang surfaces
// (e.g. /skeleton may carry both title + original_title with different
// langs) carry their tag explicitly — a downstream renderer never has
// to look up "what lang is this?" out-of-band.
type titleJSON struct {
	Value string      `json:"value"`
	Lang  LanguageTag `json:"lang"`
}

func (t Title) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(titleJSON{Value: t.value, Lang: t.lang})
}

func (t *Title) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = Title{}
		return nil
	}
	var raw titleJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("values: title unmarshal: %w", err)
	}
	parsed, err := NewTitle(raw.Value, raw.Lang)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}
