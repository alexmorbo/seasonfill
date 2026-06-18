package dto_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/shared/dto"
)

func TestPagination_PerPageOrDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   dto.Pagination
		want int
	}{
		{name: "zero_returns_default", in: dto.Pagination{PerPage: 0}, want: 20},
		{name: "negative_returns_default", in: dto.Pagination{PerPage: -1}, want: 20},
		{name: "positive_returned_as_is", in: dto.Pagination{PerPage: 50}, want: 50},
		{name: "max_returned_as_is", in: dto.Pagination{PerPage: 200}, want: 200},
		{name: "above_max_returned_as_is_validation_handles_cap", in: dto.Pagination{PerPage: 999}, want: 999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.in.PerPageOrDefault())
		})
	}
}

func TestPagination_Offset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   dto.Pagination
		want int
	}{
		{name: "page_0_offset_0", in: dto.Pagination{Page: 0, PerPage: 50}, want: 0},
		{name: "negative_page_offset_0", in: dto.Pagination{Page: -5, PerPage: 50}, want: 0},
		{name: "page_1_offset_0", in: dto.Pagination{Page: 1, PerPage: 50}, want: 0},
		{name: "page_2_default_per_page", in: dto.Pagination{Page: 2}, want: 20},
		{name: "page_3_per_page_10", in: dto.Pagination{Page: 3, PerPage: 10}, want: 20},
		{name: "page_5_per_page_50", in: dto.Pagination{Page: 5, PerPage: 50}, want: 200},
		{name: "page_4_per_page_max", in: dto.Pagination{Page: 4, PerPage: 200}, want: 600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.in.Offset())
		})
	}
}

func TestListQuery_EmbeddingFieldAccess(t *testing.T) {
	t.Parallel()

	tmdbType := 4
	q := dto.ListQuery{
		InstanceFilter: dto.InstanceFilter{Instance: "sonarr-1"},
		TmdbTypeFilter: dto.TmdbTypeFilter{TmdbType: &tmdbType},
		LanguagePref:   dto.LanguagePref{Lang: "en"},
		Pagination:     dto.Pagination{Page: 2, PerPage: 50},
	}

	// Each embedded field is reachable via the outer ListQuery without
	// qualifying the embedded struct name — go embedding contract.
	assert.Equal(t, "sonarr-1", q.Instance)
	assert.Equal(t, "en", q.Lang)
	assert.Equal(t, 2, q.Page)
	assert.Equal(t, 50, q.PerPage)
	if assert.NotNil(t, q.TmdbType) {
		assert.Equal(t, 4, *q.TmdbType)
	}

	// Embedded methods also promote.
	assert.Equal(t, 50, q.PerPageOrDefault())
	assert.Equal(t, 50, q.Offset())
}
