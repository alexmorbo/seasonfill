package clock

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Fake is a deterministic, virtual-time Clock implementation for
// tests. The virtual clock only moves when the test calls Advance(d);
// goroutines that called Sleep/NewTimer/NewTicker park on internal
// channels and only wake when Advance crosses their deadline.
//
// All exported methods are safe for concurrent use.
type Fake struct {
	mu       sync.Mutex
	now      time.Time
	waiters  int           // sleep waiters + parked timer receivers
	waiterCV *sync.Cond    // signalled when waiters changes
	pending  []*fakeWaiter // sleep + timer deadlines, sorted by .at
	tickers  []*fakeTicker // active tickers, each owns its own period state
	nextID   uint64
}

// NewFake constructs a Fake whose virtual time starts at start. Use
// a non-zero, easy-to-read start time in tests (e.g.
// time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) to make assertions
// readable.
func NewFake(start time.Time) *Fake {
	f := &Fake{now: start}
	f.waiterCV = sync.NewCond(&f.mu)
	return f
}

// Now returns the virtual time. Safe for concurrent use.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Sleep blocks for d virtual nanoseconds, OR until ctx is cancelled,
// whichever wins. d <= 0 returns nil immediately. Returns ctx.Err()
// on cancel; nil when Advance fires the waiter.
//
// The waiter is registered atomically with the parking — by the time
// Sleep increments the waiter count, the deadline is already in the
// pending list, so BlockUntilWaiters(n) will see the waiter as soon
// as the count crosses n.
func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	w := f.addWaiter(d)
	select {
	case <-ctx.Done():
		f.cancelWaiter(w)
		return ctx.Err()
	case <-w.ch:
		return nil
	}
}

// NewTimer returns a Timer fixed to fire after d virtual nanoseconds.
// Calling Stop before the fire removes it from the pending list and
// returns true; otherwise returns false (matching time.Timer.Stop
// semantics).
func (f *Fake) NewTimer(d time.Duration) Timer {
	w := f.addWaiter(d)
	return &fakeTimer{f: f, w: w}
}

// NewTicker returns a Ticker that fires every d virtual nanoseconds
// when Advance is called past its next deadline. Internally each
// ticker advances its own "next" deadline by exactly d on every fire,
// so 5×d worth of Advance delivers exactly 5 ticks (or whatever the
// channel buffer absorbs).
func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock.Fake.NewTicker: non-positive interval")
	}
	t := &fakeTicker{f: f, period: d, ch: make(chan time.Time, 1)}
	f.mu.Lock()
	t.next = f.now.Add(d)
	f.tickers = append(f.tickers, t)
	f.mu.Unlock()
	return t
}

// Advance moves virtual time forward by d and fires every waiter and
// ticker whose deadline is at or before the new now. Firing is
// deadline-ordered: a 1ns timer registered before a 2ns timer is
// always delivered to its receiver first when Advance(3ns) runs.
//
// Note: firing only signals the channel; it does NOT wait for the
// receiver goroutine to consume it. Use BlockUntilWaiters(n) before
// or after Advance to rendezvous with parked goroutines.
func (f *Fake) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	f.mu.Lock()
	target := f.now.Add(d)
	// Fire waiters AND ticker fires in deadline order so the
	// observable ordering matches a real-time scheduler.
	type pendingFire struct {
		at time.Time
		// Exactly one of these is non-nil per entry.
		waiter *fakeWaiter
		ticker *fakeTicker
	}
	var fires []pendingFire
	// Collect ready waiters; trim pending in place to keep what
	// remains.
	remaining := f.pending[:0]
	for _, w := range f.pending {
		if !w.at.After(target) {
			fires = append(fires, pendingFire{at: w.at, waiter: w})
		} else {
			remaining = append(remaining, w)
		}
	}
	f.pending = remaining
	// Collect ticker fires. A ticker may fire multiple times within
	// one Advance call if d covers several periods — we emit each
	// distinct deadline in order, but each ticker channel has a
	// 1-slot buffer (matching time.Ticker), so back-to-back fires
	// without a reader collapse into a single delivered tick. That
	// matches the standard library's drop-on-overflow semantics.
	for _, t := range f.tickers {
		for !t.next.After(target) {
			fires = append(fires, pendingFire{at: t.next, ticker: t})
			t.next = t.next.Add(t.period)
		}
	}
	sort.SliceStable(fires, func(i, j int) bool { return fires[i].at.Before(fires[j].at) })
	f.now = target
	f.mu.Unlock()
	for _, e := range fires {
		switch {
		case e.waiter != nil:
			e.waiter.fire(e.at)
		case e.ticker != nil:
			e.ticker.tick(e.at)
		}
	}
}

// BlockUntilWaiters blocks the caller until at least n goroutines are
// parked in Sleep, on a Timer.C, or on a Ticker.C. Tickers do NOT
// count as waiters — they fire on Advance regardless of whether a
// goroutine is reading from their channel. Only Sleep and Timer
// receivers (the bucket's pause loop is one of these) register a
// waiter.
//
// Used to rendezvous with goroutines that race into Sleep / Timer
// concurrently with the test driver. Pattern:
//
//	go workerThatSleepsAt(fakeClock)
//	go workerThatSleepsAt(fakeClock)
//	fakeClock.BlockUntilWaiters(2)
//	fakeClock.Advance(d)
func (f *Fake) BlockUntilWaiters(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for f.waiters < n {
		f.waiterCV.Wait()
	}
}

// fakeWaiter represents one Sleep call or one not-yet-fired Timer.
type fakeWaiter struct {
	id    uint64
	at    time.Time
	ch    chan time.Time // 1-slot buffer; receiver may already be parked
	fired bool           // guard against double-fire under race with Stop
}

// addWaiter registers a waiter and bumps the waiters counter ATOMICALLY
// with the pending-list insert. The waiter counter is incremented
// before unlocking so BlockUntilWaiters never observes a torn state
// where the deadline is published but the count hasn't grown.
//
// We intentionally count the waiter as "parked" the moment we return
// the channel — the caller is about to <-w.ch. The brief window
// between addWaiter return and the receive operation is closed by the
// channel being buffered (capacity 1) so a fire that races the
// receive is queued, not lost.
func (f *Fake) addWaiter(d time.Duration) *fakeWaiter {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	w := &fakeWaiter{
		id: f.nextID,
		at: f.now.Add(d),
		ch: make(chan time.Time, 1),
	}
	// Keep f.pending sorted by .at — Advance relies on this for the
	// ordered-fire guarantee.
	idx := sort.Search(len(f.pending), func(i int) bool {
		return f.pending[i].at.After(w.at)
	})
	f.pending = append(f.pending, nil)
	copy(f.pending[idx+1:], f.pending[idx:])
	f.pending[idx] = w
	f.waiters++
	f.waiterCV.Broadcast()
	return w
}

// cancelWaiter removes w from pending (if still present) and
// decrements waiters. Called from Sleep's ctx.Done branch.
func (f *Fake) cancelWaiter(w *fakeWaiter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, p := range f.pending {
		if p == w {
			f.pending = append(f.pending[:i], f.pending[i+1:]...)
			f.waiters--
			f.waiterCV.Broadcast()
			return
		}
	}
	// Not in pending — already fired. Waiter count was decremented at
	// fire time, nothing more to do.
}

// fire delivers the tick and decrements waiters. Called by Advance.
func (w *fakeWaiter) fire(at time.Time) {
	// Channel is buffered (cap 1); the send never blocks.
	select {
	case w.ch <- at:
	default:
	}
	w.fired = true
}

// fakeTimer wraps a fakeWaiter to expose Timer's Stop semantics.
type fakeTimer struct {
	f *Fake
	w *fakeWaiter
}

func (t *fakeTimer) C() <-chan time.Time { return t.w.ch }

// Stop removes the timer from pending if it has not yet fired. Returns
// true when Stop prevented the fire; false otherwise (already fired
// or already stopped). Matches time.Timer.Stop semantics.
func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	for i, p := range t.f.pending {
		if p == t.w {
			t.f.pending = append(t.f.pending[:i], t.f.pending[i+1:]...)
			t.f.waiters--
			t.f.waiterCV.Broadcast()
			return true
		}
	}
	return false
}

// fakeTicker is a periodic fire on its own channel. Not represented
// as a pending waiter because tickers fire on Advance regardless of
// whether a goroutine is reading from C(). The bucket's refill loop
// in production uses a ticker; we want Advance(period*N) to deliver
// N ticks even if the refill goroutine is between iterations.
type fakeTicker struct {
	f       *Fake
	period  time.Duration
	next    time.Time
	ch      chan time.Time
	stopped bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }

func (t *fakeTicker) Stop() {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if t.stopped {
		return
	}
	t.stopped = true
	for i, x := range t.f.tickers {
		if x == t {
			t.f.tickers = append(t.f.tickers[:i], t.f.tickers[i+1:]...)
			return
		}
	}
}

// tick is called by Advance. Non-blocking send — channel buffer (cap 1)
// matches time.Ticker's drop-on-overflow contract.
func (t *fakeTicker) tick(at time.Time) {
	select {
	case t.ch <- at:
	default:
	}
}
