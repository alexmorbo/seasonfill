package decision

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestNew(t *testing.T) {
	t.Parallel()

	scanID := uuid.New()
	before := time.Now().UTC().Add(-time.Second)
	d := New(scanID, "main", "Hijack", 122, 2)
	after := time.Now().UTC().Add(time.Second)

	assert.NotEqual(t, uuid.Nil, d.ID)
	assert.NotEqual(t, scanID, d.ID, "decision ID must be a fresh UUID, not the scan ID")
	assert.Equal(t, scanID, d.ScanRunID)
	assert.Equal(t, domain.InstanceName("main"), d.InstanceName)
	assert.Equal(t, 122, d.SeriesID)
	assert.Equal(t, "Hijack", d.SeriesTitle)
	assert.Equal(t, 2, d.SeasonNumber)
	assert.False(t, d.CreatedAt.Before(before))
	assert.False(t, d.CreatedAt.After(after))
	assert.Equal(t, time.UTC, d.CreatedAt.Location())
}

func TestOutcome_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Outcome("grab"), OutcomeGrab)
	assert.Equal(t, Outcome("skip"), OutcomeSkip)
	assert.Equal(t, Outcome("error"), OutcomeError)
}

func TestFilteredCandidate_StructFields(t *testing.T) {
	t.Parallel()
	fc := FilteredCandidate{
		GUID:       "g1",
		Title:      "Some Title",
		Indexer:    "RT",
		Reason:     "test",
		Quality:    "WEBDL-2160p",
		Coverage:   3,
		Rejections: []string{"x"},
	}
	assert.Equal(t, "g1", fc.GUID)
	assert.Equal(t, "RT", fc.Indexer)
	assert.Equal(t, 3, fc.Coverage)
	assert.Equal(t, []string{"x"}, fc.Rejections)
}
