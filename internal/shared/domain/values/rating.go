package values

import (
	"encoding/json"
	"fmt"
)

type Rating struct {
	score Score
	votes VoteCount
}

func NewRating(score Score, votes VoteCount) (Rating, error) {
	if score.IsZero() && votes.IsZero() {
		return Rating{}, fmt.Errorf("%w: both score and votes are zero", ErrScoreInvalid)
	}
	return Rating{score: score, votes: votes}, nil
}

func (r Rating) Score() Score        { return r.score }
func (r Rating) Votes() VoteCount    { return r.votes }
func (r Rating) IsZero() bool        { return r.score.IsZero() && r.votes.IsZero() }
func (r Rating) Equal(o Rating) bool { return r.score.Equal(o.score) && r.votes.Equal(o.votes) }
func (r Rating) String() string      { return fmt.Sprintf("%s (%s)", r.score, r.votes) }

type ratingJSON struct {
	Score Score     `json:"score"`
	Votes VoteCount `json:"votes"`
}

func (r Rating) MarshalJSON() ([]byte, error) {
	if r.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(ratingJSON{Score: r.score, Votes: r.votes})
}

func (r *Rating) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*r = Rating{}
		return nil
	}
	var raw ratingJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("values: rating unmarshal: %w", err)
	}
	parsed, err := NewRating(raw.Score, raw.Votes)
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}
