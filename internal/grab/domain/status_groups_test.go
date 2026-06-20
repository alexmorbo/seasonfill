package grab_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
)

func TestFailStatuses_Contents(t *testing.T) {
	t.Parallel()
	got := grab.FailStatuses()
	assert.ElementsMatch(t,
		[]grab.Status{grab.StatusImportFailed, grab.StatusGrabFailed},
		got,
	)
}

func TestStatusGroup_Constants(t *testing.T) {
	t.Parallel()
	// Defensive: the int values are used as a map key in the SQL
	// builder. If anyone reorders the iota the tests fail loudly.
	assert.Equal(t, grab.StatusGroup(0), grab.GroupGrabs)
	assert.Equal(t, grab.StatusGroup(1), grab.GroupImports)
	assert.Equal(t, grab.StatusGroup(2), grab.GroupFails)
}
