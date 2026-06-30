package values

import (
	"encoding/json"
	"fmt"
)

type Minutes struct {
	value int
}

func NewMinutes(v int) (Minutes, error) {
	if v <= 0 {
		return Minutes{}, fmt.Errorf("%w: got %d", ErrMinutesInvalid, v)
	}
	return Minutes{value: v}, nil
}

func (m Minutes) Value() int           { return m.value }
func (m Minutes) IsZero() bool         { return m.value == 0 }
func (m Minutes) Equal(o Minutes) bool { return m.value == o.value }
func (m Minutes) String() string       { return fmt.Sprintf("%d", m.value) }

func (m Minutes) MarshalJSON() ([]byte, error) {
	if m.value == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(m.value)
}

func (m *Minutes) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*m = Minutes{}
		return nil
	}
	var v int
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("values: minutes unmarshal: %w", err)
	}
	parsed, err := NewMinutes(v)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
