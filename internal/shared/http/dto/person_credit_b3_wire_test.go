package dto_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// B-3 wire contract assertion (Story 492 / N-1b).
//
// The TMDB person filmography "Other titles" payload and the
// per-series Cast page crew rows expose three fields whose presence
// the FE depends on. These tests lock the JSON tag names and the
// omitempty semantics so a future field rename or accidental
// omitempty drop doesn't silently break the wire shape.
//
// Background: BE columns (`person_credits.department`,
// `person_credits.original_title`, `person_credits.vote_count`) and
// the matching DTO fields landed in story 307. Story 492 verifies and
// locks the wire contract — no behavioural change.

func TestCrewPageMember_DepartmentWireKey(t *testing.T) {
	dep := "Production"
	m := dto.CrewPageMember{Department: &dep}
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"department":"Production"`)

	// Nil Department → key absent (omitempty).
	m.Department = nil
	raw, err = json.Marshal(m)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"department"`)
}

func TestOtherCreditEntry_B3FieldsWireKeys(t *testing.T) {
	dep := "Writing"
	origTitle := "Yôjinbô"
	votes := 1234
	e := dto.OtherCreditEntry{
		TMDBMediaID:   999,
		MediaType:     "tv",
		Title:         "Yojimbo",
		Kind:          "cast",
		RoleLabel:     "Writer",
		Department:    &dep,
		OriginalTitle: &origTitle,
		VoteCount:     &votes,
	}
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	s := string(raw)
	assert.Contains(t, s, `"department":"Writing"`)
	assert.Contains(t, s, `"original_title":"Yôjinbô"`)
	assert.Contains(t, s, `"vote_count":1234`)
}

func TestOtherCreditEntry_B3FieldsOmitWhenNil(t *testing.T) {
	e := dto.OtherCreditEntry{
		TMDBMediaID: 999,
		MediaType:   "tv",
		Title:       "T",
		Kind:        "cast",
		RoleLabel:   "X",
	}
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	s := string(raw)
	assert.NotContains(t, s, `"department"`)
	assert.NotContains(t, s, `"original_title"`)
	assert.NotContains(t, s, `"vote_count"`)
}

// Story 537 (B-42e) — `series_id` is the canonical series row id
// returned when the underlying TMDB media has a canon row in the
// database, even if no live cache references exist. FE uses it to
// route to /series/:id (Global Composer w/ TMDBFallbackUseCase).
// Pointer with omitempty so it never leaks empty values.
func TestOtherCreditEntry_SeriesID_WirePresentWhenSet(t *testing.T) {
	t.Parallel()
	sid := domain.SeriesID(777)
	e := dto.OtherCreditEntry{
		TMDBMediaID: 999,
		MediaType:   "tv",
		Title:       "x",
		Kind:        "cast",
		RoleLabel:   "",
		SeriesID:    &sid,
	}
	b, err := json.Marshal(e)
	require.NoError(t, err)
	require.Contains(t, string(b), `"series_id":777`)
}

func TestOtherCreditEntry_SeriesID_WireOmittedWhenNil(t *testing.T) {
	t.Parallel()
	e := dto.OtherCreditEntry{
		TMDBMediaID: 999,
		MediaType:   "tv",
		Title:       "x",
		Kind:        "cast",
		RoleLabel:   "",
		// SeriesID intentionally nil
	}
	b, err := json.Marshal(e)
	require.NoError(t, err)
	require.NotContains(t, string(b), `"series_id"`)
}
