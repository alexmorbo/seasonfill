package database

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestScanRunModel_TableName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "scan_runs", ScanRunModel{}.TableName())
}

func TestDecisionModel_TableName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "decisions", DecisionModel{}.TableName())
}

func TestNewScanID(t *testing.T) {
	t.Parallel()

	id := NewScanID()
	parsed, err := uuid.Parse(id)
	assert.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, parsed)

	// Two calls produce distinct IDs.
	assert.NotEqual(t, NewScanID(), NewScanID())
}
