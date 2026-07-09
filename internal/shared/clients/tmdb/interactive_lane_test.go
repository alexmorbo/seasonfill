package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/clock"
)

// mustNewFrac constructs a Client with an explicit RPS + interactive reserve
// fraction + injected clock. White-box (package tmdb) so tests can reach the
// unexported coldGate / limiter fields, mirroring the existing client_test.go
// pattern.
func mustNewFrac(t *testing.T, base, tok string, clk clock.Clock, rps, frac float64) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:                base,
		Token:                  tok,
		Language:               "en-US",
		HTTPClient:             &http.Client{Timeout: 5 * time.Second},
		RPS:                    rps,
		InteractiveReserveFrac: frac,
		Clock:                  clk,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// drainBucket empties a token bucket's pre-filled tokens without blocking.
// Under a fake clock with no Advance the refill ticker never fires, so the
// bucket stays empty after this returns.
func drainBucket(tb *tokenBucket) {
	for {
		select {
		case <-tb.tokens:
		default:
			return
		}
	}
}

// TEST 1 — Headroom under batch saturation + batch throttling, deterministic.
// With the cold gate exhausted (batch lane saturated) and the fake clock never
// advanced (so the cold gate cannot refill):
//   - an interactive-marked acquire MUST proceed immediately (shared bucket
//     only) → headroom held;
//   - a batch (unmarked) acquire MUST block on the exhausted cold gate →
//     throttled — proven by cancelling its ctx to release it.
func TestInteractiveLane_HeadroomHeldWhileBatchThrottled(t *testing.T) {
	fc := clock.NewFake(fakeStart)
	c := mustNewFrac(t, "http://example.invalid", "tk", fc, 50, 0.25)
	defer c.Close()

	// Saturate the batch lane: empty the cold gate. No Advance → no refill.
	drainBucket(c.coldGate)

	// Interactive acquire draws only from the (pre-filled) shared bucket.
	ictx := WithInteractive(context.Background())
	iDone := make(chan error, 1)
	go func() { iDone <- c.acquire(ictx, isInteractive(ictx)) }()
	select {
	case err := <-iDone:
		if err != nil {
			t.Fatalf("interactive acquire: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interactive acquire blocked under cold-gate saturation — headroom lost")
	}

	// Batch acquire on the exhausted cold gate must block.
	bctx, cancel := context.WithCancel(context.Background())
	bDone := make(chan error, 1)
	go func() { bDone <- c.acquire(bctx, false) }()
	select {
	case <-bDone:
		t.Fatal("batch acquire returned though cold gate exhausted — not throttled")
	case <-time.After(150 * time.Millisecond):
		// still blocked — correct
	}
	cancel()
	if err := <-bDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("batch acquire err = %v, want context.Canceled", err)
	}
}

// TEST 2 — End-to-end throttle contrast over real HTTP + real clock. Mirrors
// the existing TestClient_RateLimiter_EnvOverride timing style.
//
// RPS=9 → shared bucket 9 rps; frac=0.5 → cold gate 4.5 rps. Ten batch calls
// are gated by the stricter cold gate (a no-cold-gate baseline would finish in
// ~555ms on the shared bucket alone); the >=950ms floor isolates the cold-gate
// throttle. The same ten calls marked interactive hit only the shared bucket
// (~555ms) and must beat an 850ms ceiling — proving interactive keeps headroom.
func TestInteractiveLane_BatchThrottled_InteractiveFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{
		BaseURL:                srv.URL,
		Token:                  "tk",
		Language:               "en-US",
		HTTPClient:             &http.Client{Timeout: 5 * time.Second},
		RPS:                    9,
		InteractiveReserveFrac: 0.5,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	// Batch lane — distinct ids so every call misses the SWR cache and reaches
	// doDirect.
	startB := time.Now()
	for i := range 10 {
		if _, err := c.GetTV(context.Background(), int64(i), ""); err != nil {
			t.Fatalf("batch call %d: %v", i, err)
		}
	}
	batchElapsed := time.Since(startB)
	if batchElapsed < 950*time.Millisecond {
		t.Fatalf("batch 10 calls = %v; cold gate not throttling (want >= 950ms; shared-only baseline ~555ms)", batchElapsed)
	}

	// Interactive lane — shared bucket refilled during the batch run; ten marked
	// calls run at shared-only speed.
	ictx := WithInteractive(context.Background())
	startI := time.Now()
	for i := range 10 {
		if _, err := c.GetTV(ictx, int64(1000+i), ""); err != nil {
			t.Fatalf("interactive call %d: %v", i, err)
		}
	}
	interactiveElapsed := time.Since(startI)
	if interactiveElapsed >= 850*time.Millisecond {
		t.Fatalf("interactive 10 calls = %v; expected shared-only speed < 850ms (headroom lost)", interactiveElapsed)
	}
}

// TEST 3 — Marker default + propagation semantics. A bare ctx is batch; only
// WithInteractive marks; the mark survives a derived child (WithTimeout/
// WithValue) — which is why it rides the freshener's detached-with-timeout ctx
// and the whole worker call chain — but a fresh Background() does NOT inherit
// it (documents why SWR background refresh, which reparents to Background,
// correctly falls to the batch lane).
func TestInteractiveMarker_DefaultBatch_And_Propagation(t *testing.T) {
	if isInteractive(context.Background()) {
		t.Fatal("bare context must default to the batch lane")
	}
	ictx := WithInteractive(context.Background())
	if !isInteractive(ictx) {
		t.Fatal("WithInteractive must mark the ctx interactive")
	}
	child, cancel := context.WithTimeout(ictx, time.Second)
	defer cancel()
	if !isInteractive(child) {
		t.Fatal("marker must survive a derived child ctx (freshener detach + worker chain)")
	}
	if isInteractive(context.Background()) {
		t.Fatal("a fresh Background() ctx must not inherit the marker (SWR bg-refresh path)")
	}
}

// TEST 5 — Both lanes honour the 429 global pause. The pause lives on the shared
// bucket; interactive hits it directly, batch hits it after clearing the
// (un-paused, pre-filled) cold gate. Under a fake clock both acquires park on
// the shared pause timer and only proceed once the window is Advanced past.
func TestInteractiveLane_BothLanesHonorGlobalPause(t *testing.T) {
	fc := clock.NewFake(fakeStart)
	c := mustNewFrac(t, "http://example.invalid", "tk", fc, 1000, 0.25)
	defer c.Close()

	// Open a 1s global pause on the shared bucket (the 429 chokepoint).
	if !c.limiter.PauseUntil(fc.Now().Add(time.Second)) {
		t.Fatal("PauseUntil should open a fresh window")
	}

	ictx := WithInteractive(context.Background())
	iDone := make(chan error, 1)
	bDone := make(chan error, 1)
	go func() { iDone <- c.acquire(ictx, true) }()
	go func() { bDone <- c.acquire(context.Background(), false) }()

	// Parked Timer waiters on the shared pause: watchResume(1) + interactive(1)
	// + batch(1) = 3. Batch clears the pre-filled cold gate without parking
	// (immediate token, no pause on the cold gate), then parks on the shared
	// pause. Tickers do NOT count as waiters (see clock.Fake.BlockUntilWaiters).
	fc.BlockUntilWaiters(3)

	select {
	case <-iDone:
		t.Fatal("interactive proceeded during global pause")
	default:
	}
	select {
	case <-bDone:
		t.Fatal("batch proceeded during global pause")
	default:
	}

	// Lift the pause.
	fc.Advance(time.Second)
	for _, tc := range []struct {
		name string
		ch   chan error
	}{{"interactive", iDone}, {"batch", bDone}} {
		select {
		case err := <-tc.ch:
			if err != nil {
				t.Fatalf("%s acquire after pause: %v", tc.name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s lane did not resume after pause window elapsed", tc.name)
		}
	}
}
