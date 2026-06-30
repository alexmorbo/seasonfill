package values

import (
	"encoding/json"
	"fmt"
)

type Year struct {
	value int
}

func NewYear(v int) (Year, error) {
	if v < 1900 || v > 2100 {
		return Year{}, fmt.Errorf("%w: got %d", ErrYearInvalid, v)
	}
	return Year{value: v}, nil
}

func (y Year) Value() int        { return y.value }
func (y Year) IsZero() bool      { return y.value == 0 }
func (y Year) Equal(o Year) bool { return y.value == o.value }
func (y Year) String() string    { return fmt.Sprintf("%d", y.value) }

func (y Year) MarshalJSON() ([]byte, error) {
	if y.value == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(y.value)
}

func (y *Year) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*y = Year{}
		return nil
	}
	var v int
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("values: year unmarshal: %w", err)
	}
	parsed, err := NewYear(v)
	if err != nil {
		return err
	}
	*y = parsed
	return nil
}
