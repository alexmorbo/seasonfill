package values

import (
	"encoding/json"
	"fmt"
)

// SeriesStatus mirrors the TMDB/Sonarr series status field. The value
// set is fixed (the providers emit a closed list) — adding a new
// status requires a one-line update to allowedSeriesStatuses.
type SeriesStatus struct {
	value string
}

var allowedSeriesStatuses = map[string]struct{}{
	"Returning Series": {},
	"Ended":            {},
	"Canceled":         {},
	"In Production":    {},
	"Pilot":            {},
	"Planned":          {},
}

func NewSeriesStatus(s string) (SeriesStatus, error) {
	if _, ok := allowedSeriesStatuses[s]; !ok {
		return SeriesStatus{}, fmt.Errorf("%w: got %q", ErrSeriesStatusInvalid, s)
	}
	return SeriesStatus{value: s}, nil
}

func (s SeriesStatus) Value() string             { return s.value }
func (s SeriesStatus) IsZero() bool              { return s.value == "" }
func (s SeriesStatus) Equal(o SeriesStatus) bool { return s.value == o.value }
func (s SeriesStatus) String() string            { return s.value }

func (s SeriesStatus) MarshalJSON() ([]byte, error) {
	if s.value == "" {
		return []byte("null"), nil
	}
	return json.Marshal(s.value)
}

func (s *SeriesStatus) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = SeriesStatus{}
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("values: series_status unmarshal: %w", err)
	}
	parsed, err := NewSeriesStatus(raw)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}
