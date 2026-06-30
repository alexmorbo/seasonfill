package values

import (
	"encoding/json"
	"fmt"
)

type VoteCount struct {
	value int
}

func NewVoteCount(v int) (VoteCount, error) {
	if v < 0 {
		return VoteCount{}, fmt.Errorf("%w: got %d", ErrVoteCountInvalid, v)
	}
	return VoteCount{value: v}, nil
}

func (v VoteCount) Value() int             { return v.value }
func (v VoteCount) IsZero() bool           { return v.value == 0 }
func (v VoteCount) Equal(o VoteCount) bool { return v.value == o.value }
func (v VoteCount) String() string         { return fmt.Sprintf("%d", v.value) }

func (v VoteCount) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.value)
}

func (v *VoteCount) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*v = VoteCount{}
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("values: vote_count unmarshal: %w", err)
	}
	parsed, err := NewVoteCount(n)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}
