package grab

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBackoffFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		attempt int
		max     time.Duration
		want    time.Duration
	}{
		{"attempt 1", 1, 30 * time.Second, time.Second},
		{"attempt 2", 2, 30 * time.Second, 5 * time.Second},
		{"attempt 3", 3, 30 * time.Second, 30 * time.Second},
		{"attempt 4 stays at cap", 4, 30 * time.Second, 30 * time.Second},
		{"cap below natural", 2, 2 * time.Second, 2 * time.Second},
		{"zero max defaults", 1, 0, time.Second},
		{"attempt zero behaves like 1", 0, 30 * time.Second, time.Second},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, backoffFor(tt.attempt, tt.max))
		})
	}
}
