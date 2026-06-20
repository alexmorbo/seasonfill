package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModels_TableNames(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "scan_runs", ScanRunModel{}.TableName())
	assert.Equal(t, "decisions", DecisionModel{}.TableName())
	assert.Equal(t, "grab_records", GrabRecordModel{}.TableName())
	assert.Equal(t, "origin_releases", OriginReleaseModel{}.TableName())
	assert.Equal(t, "cooldowns", CooldownModel{}.TableName())
}

func TestNewScanID_NotEmpty(t *testing.T) {
	t.Parallel()
	id := NewScanID()
	assert.NotEmpty(t, id)
	assert.Len(t, id, 36)
}
