package values

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// LanguageTag holds a BCP-47 language tag (e.g. "ru-RU", "en-US"). The
// canonical form is lowercase language + "-" + uppercase region; the
// constructor normalizes input.
type LanguageTag struct {
	value string
}

// bcp47Pattern matches the subset of BCP-47 that seasonfill emits: two
// lowercase letters + "-" + two uppercase letters. Wider BCP-47 (script
// subtags, variants) is not in scope.
var bcp47Pattern = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}$`)

func NewLanguageTag(s string) (LanguageTag, error) {
	s = strings.TrimSpace(s)
	if len(s) == 5 && s[2] == '-' {
		s = strings.ToLower(s[:2]) + "-" + strings.ToUpper(s[3:])
	}
	if !bcp47Pattern.MatchString(s) {
		return LanguageTag{}, fmt.Errorf("%w: got %q", ErrLanguageTagInvalid, s)
	}
	return LanguageTag{value: s}, nil
}

func (l LanguageTag) Value() string            { return l.value }
func (l LanguageTag) IsZero() bool             { return l.value == "" }
func (l LanguageTag) Equal(o LanguageTag) bool { return l.value == o.value }
func (l LanguageTag) String() string           { return l.value }

func (l LanguageTag) MarshalJSON() ([]byte, error) {
	if l.value == "" {
		return []byte("null"), nil
	}
	return json.Marshal(l.value)
}

func (l *LanguageTag) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*l = LanguageTag{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("values: language_tag unmarshal: %w", err)
	}
	parsed, err := NewLanguageTag(s)
	if err != nil {
		return err
	}
	*l = parsed
	return nil
}
