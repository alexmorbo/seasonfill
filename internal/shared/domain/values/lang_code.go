package values

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// LangCode is the ISO 639-1 two-letter language code, lowercase
// ("ru", "en"). Distinct from LanguageTag (BCP-47 "ru-RU") — LangCode
// is the bare language family; LanguageTag is language+region for
// localized renders.
type LangCode struct {
	value string
}

var iso639Pattern = regexp.MustCompile(`^[a-z]{2}$`)

func NewLangCode(s string) (LangCode, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !iso639Pattern.MatchString(s) {
		return LangCode{}, fmt.Errorf("%w: got %q", ErrLangCodeInvalid, s)
	}
	return LangCode{value: s}, nil
}

func (l LangCode) Value() string         { return l.value }
func (l LangCode) IsZero() bool          { return l.value == "" }
func (l LangCode) Equal(o LangCode) bool { return l.value == o.value }
func (l LangCode) String() string        { return l.value }

func (l LangCode) MarshalJSON() ([]byte, error) {
	if l.value == "" {
		return []byte("null"), nil
	}
	return json.Marshal(l.value)
}

func (l *LangCode) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*l = LangCode{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("values: lang_code unmarshal: %w", err)
	}
	parsed, err := NewLangCode(s)
	if err != nil {
		return err
	}
	*l = parsed
	return nil
}
