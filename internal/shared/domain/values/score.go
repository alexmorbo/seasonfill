package values

import (
	"encoding/json"
	"fmt"
	"math"
)

type Score struct {
	value float64
}

func NewScore(v float64) (Score, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 10 {
		return Score{}, fmt.Errorf("%w: got %v", ErrScoreInvalid, v)
	}
	return Score{value: v}, nil
}

func (s Score) Value() float64     { return s.value }
func (s Score) IsZero() bool       { return s.value == 0 }
func (s Score) Equal(o Score) bool { return s.value == o.value }
func (s Score) String() string     { return fmt.Sprintf("%.1f", s.value) }

func (s Score) MarshalJSON() ([]byte, error) {
	if s.value == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(s.value)
}

func (s *Score) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = Score{}
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("values: score unmarshal: %w", err)
	}
	parsed, err := NewScore(v)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}
