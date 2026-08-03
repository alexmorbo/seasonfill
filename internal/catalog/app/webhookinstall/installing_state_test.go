package webhookinstall

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

// Test 1: fresh instance, first attempt fails inside the grace window →
// Installing=true, LastError populated, Installed=false, Attempts==1,
// FirstAttemptAt stamped to now.
func TestReconcile_InstallingDuringGraceFirstFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{
		createResp:         sonarr.Notification{ID: 7, Implementation: "Webhook"},
		testNotificationFn: func(sonarr.NotificationPayload) error { return errors.New("sonarr could not reach seasonfill") },
	}
	r, _ := newReconciler(t, snap, n, "https://sf.example")
	r = r.WithClock(func() time.Time { return now })

	st, err := r.Reconcile(context.Background(), "alpha")
	if err == nil {
		t.Fatalf("expected underlying error from the failed test round-trip")
	}
	if !st.Installing {
		t.Fatalf("first in-grace failure must read as Installing: %+v", st)
	}
	if st.Installed {
		t.Fatalf("must not be Installed while installing: %+v", st)
	}
	if st.LastError == nil {
		t.Fatalf("LastError must stay populated (underlying cause): %+v", st)
	}
	if st.Attempts != 1 {
		t.Fatalf("Attempts: got %d want 1", st.Attempts)
	}
	if st.FirstAttemptAt.IsZero() || !st.FirstAttemptAt.Equal(now) {
		t.Fatalf("FirstAttemptAt must be stamped to now: %v", st.FirstAttemptAt)
	}
}

// Test 2: the 4th consecutive failure (Attempts>3) clears Installing even
// though the 10-minute window is still open — the error surfaces.
func TestReconcile_InstallingClearsAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{
		createResp:         sonarr.Notification{ID: 7, Implementation: "Webhook"},
		testNotificationFn: func(sonarr.NotificationPayload) error { return errors.New("still not reachable") },
	}
	r, _ := newReconciler(t, snap, n, "https://sf.example")
	r = r.WithClock(func() time.Time { return now }) // clock frozen → isolate the attempts cap

	var st Status
	for range 4 {
		st, _ = r.Reconcile(context.Background(), "alpha")
	}
	if st.Attempts != 4 {
		t.Fatalf("Attempts: got %d want 4", st.Attempts)
	}
	if st.Installing {
		t.Fatalf("after >3 attempts must not be Installing: %+v", st)
	}
	if st.LastError == nil {
		t.Fatalf("real error must surface once grace ends: %+v", st)
	}
}

// Test 3: grace elapses by the clock — the 2nd failure at
// FirstAttemptAt+10m clears Installing even though Attempts (2) <= 3.
func TestReconcile_InstallingClearsWhenGraceWindowElapses(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	cur := t0
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	n := &fakeNotifier{
		createResp:         sonarr.Notification{ID: 7, Implementation: "Webhook"},
		testNotificationFn: func(sonarr.NotificationPayload) error { return errors.New("not reachable yet") },
	}
	r, _ := newReconciler(t, snap, n, "https://sf.example")
	r = r.WithClock(func() time.Time { return cur })

	st1, _ := r.Reconcile(context.Background(), "alpha")
	if !st1.Installing {
		t.Fatalf("first failure in grace should be Installing: %+v", st1)
	}

	cur = t0.Add(GraceWindow) // exactly +10m → elapsed == GraceWindow → NOT < GraceWindow
	st2, _ := r.Reconcile(context.Background(), "alpha")
	if st2.Attempts != 2 {
		t.Fatalf("Attempts: got %d want 2", st2.Attempts)
	}
	if st2.Installing {
		t.Fatalf("grace window elapsed → must not be Installing: %+v", st2)
	}
	if st2.LastError == nil {
		t.Fatalf("real error must surface after grace: %+v", st2)
	}
	if !st2.FirstAttemptAt.Equal(t0) {
		t.Fatalf("FirstAttemptAt must stay anchored to the first failure: %v", st2.FirstAttemptAt)
	}
}

// Test 4: a success after prior failures resets install state entirely —
// Attempts=0, FirstAttemptAt zero, Installing=false, LastError=nil,
// Installed=true.
func TestReconcile_SuccessAfterFailuresResetsInstallState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	snap := runtime.InstanceSnapshot{Name: "alpha", WebhookInstallEnabled: true}
	failTest := true
	n := &fakeNotifier{
		createResp: sonarr.Notification{ID: 7, Implementation: "Webhook"},
		testNotificationFn: func(sonarr.NotificationPayload) error {
			if failTest {
				return errors.New("not reachable yet")
			}
			return nil
		},
	}
	r, _ := newReconciler(t, snap, n, "https://sf.example")
	r = r.WithClock(func() time.Time { return now })

	st1, _ := r.Reconcile(context.Background(), "alpha")
	if !st1.Installing || st1.Attempts != 1 {
		t.Fatalf("setup expected Installing/Attempts=1: %+v", st1)
	}

	failTest = false
	st2, err := r.Reconcile(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("unexpected err on success: %v", err)
	}
	if !st2.Installed {
		t.Fatalf("must be Installed after success: %+v", st2)
	}
	if st2.Installing {
		t.Fatalf("Installing must reset to false on success: %+v", st2)
	}
	if st2.LastError != nil {
		t.Fatalf("LastError must clear on success: %+v", st2)
	}
	if st2.Attempts != 0 {
		t.Fatalf("Attempts must reset to 0, got %d", st2.Attempts)
	}
	if !st2.FirstAttemptAt.IsZero() {
		t.Fatalf("FirstAttemptAt must reset to zero, got %v", st2.FirstAttemptAt)
	}
}
