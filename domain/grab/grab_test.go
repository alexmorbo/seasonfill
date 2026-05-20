package grab

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestStatus_Constants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Status("grabbed"), StatusGrabbed)
	assert.Equal(t, Status("grab_failed"), StatusGrabFailed)
	assert.Equal(t, Status("imported"), StatusImported)
	assert.Equal(t, Status("import_failed"), StatusImportFailed)
}

func TestRecord_Fields(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	r := Record{ID: id, Status: StatusGrabbed, Attempts: 1}
	assert.Equal(t, id, r.ID)
	assert.Equal(t, StatusGrabbed, r.Status)
	assert.Equal(t, 1, r.Attempts)
}
