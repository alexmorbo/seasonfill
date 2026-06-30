package values

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type CountryCode struct {
	value string
}

var iso3166Pattern = regexp.MustCompile(`^[A-Z]{2}$`)

func NewCountryCode(s string) (CountryCode, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !iso3166Pattern.MatchString(s) {
		return CountryCode{}, fmt.Errorf("%w: got %q", ErrCountryCodeInvalid, s)
	}
	return CountryCode{value: s}, nil
}

func (c CountryCode) Value() string            { return c.value }
func (c CountryCode) IsZero() bool             { return c.value == "" }
func (c CountryCode) Equal(o CountryCode) bool { return c.value == o.value }
func (c CountryCode) String() string           { return c.value }

func (c CountryCode) MarshalJSON() ([]byte, error) {
	if c.value == "" {
		return []byte("null"), nil
	}
	return json.Marshal(c.value)
}

func (c *CountryCode) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*c = CountryCode{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("values: country_code unmarshal: %w", err)
	}
	parsed, err := NewCountryCode(s)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}
