package persistence

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDedupInt64Preserve(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []int64
		want []int64
	}{
		{"nil", nil, nil},
		{"empty", []int64{}, []int64{}},
		{"single", []int64{7}, []int64{7}},
		{"no_dupes", []int64{10, 20, 30}, []int64{10, 20, 30}},
		{"first_occurrence_order", []int64{10, 20, 10, 30, 20}, []int64{10, 20, 30}},
		{"all_same", []int64{5, 5, 5}, []int64{5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, dedupInt64Preserve(tc.in))
		})
	}
}
