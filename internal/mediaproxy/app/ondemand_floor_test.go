package media

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// effectiveBudget returns how much wall-clock budget the child ctx has.
func effectiveBudget(t *testing.T, ctx context.Context) time.Duration {
	t.Helper()
	dl, ok := ctx.Deadline()
	require.True(t, ok, "child ctx must carry a deadline")
	return time.Until(dl)
}

// W19-1 (c) / H1 — with a 10s parent + 10s floor, contextWithFloorTimeout
// yields a ~10s effective budget. This is the fix: pre-W19-1 the floor was
// 1.5s and CAPPED the larger parent budget down to 1.5s.
func TestContextWithFloorTimeout_TenSecondNotFloored(t *testing.T) {
	parent, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pcancel()

	child, cancel := contextWithFloorTimeout(parent, 10*time.Second)
	defer cancel()

	got := effectiveBudget(t, child)
	require.Greater(t, got, 9*time.Second, "10s parent + 10s floor must keep ~10s, not collapse")
	require.LessOrEqual(t, got, 10*time.Second+50*time.Millisecond)
}

// W19-1 (c) — no parent deadline: the floor is applied directly (~10s).
func TestContextWithFloorTimeout_NoParentDeadline(t *testing.T) {
	child, cancel := contextWithFloorTimeout(context.Background(), 10*time.Second)
	defer cancel()

	got := effectiveBudget(t, child)
	require.Greater(t, got, 9*time.Second)
	require.LessOrEqual(t, got, 10*time.Second+50*time.Millisecond)
}

// W19-1 (c) — a TIGHTER parent still wins (MIN semantics): 500ms parent
// under a 10s floor yields ~500ms, never 10s.
func TestContextWithFloorTimeout_TighterParentWins(t *testing.T) {
	parent, pcancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer pcancel()

	child, cancel := contextWithFloorTimeout(parent, 10*time.Second)
	defer cancel()

	got := effectiveBudget(t, child)
	require.LessOrEqual(t, got, 500*time.Millisecond+10*time.Millisecond, "tighter parent deadline must win")
}

// W19-1 (c) — DOCUMENTS the pre-W19-1 bug: a LOOSER parent (10s) under a
// SMALL floor (1.5s) is capped DOWN to the floor. This is exactly why
// raising only the handler wall budget was a no-op; the guard proves the
// CEILING semantics we now feed a 10s floor into.
func TestContextWithFloorTimeout_SmallFloorCapsLooseParent(t *testing.T) {
	parent, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pcancel()

	child, cancel := contextWithFloorTimeout(parent, 1500*time.Millisecond)
	defer cancel()

	got := effectiveBudget(t, child)
	require.LessOrEqual(t, got, 1500*time.Millisecond+20*time.Millisecond,
		"loose parent must be capped down to the (small) floor — the CEILING semantics")
}
