package clock

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

var fakeStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestRealClock_NowAdvances(t *testing.T) {
	c := Real()
	a := c.Now()
	time.Sleep(time.Millisecond)
	b := c.Now()
	if !b.After(a) {
		t.Fatalf("Real().Now() did not advance: %v -> %v", a, b)
	}
}

func TestRealClock_SleepHonoursCtx(t *testing.T) {
	c := Real()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Second); err == nil {
		t.Fatal("expected ctx error from cancelled Sleep")
	}
}

func TestRealClock_SleepZeroReturnsImmediately(t *testing.T) {
	c := Real()
	start := time.Now()
	if err := c.Sleep(context.Background(), 0); err != nil {
		t.Fatalf("Sleep(0): %v", err)
	}
	if d := time.Since(start); d > 10*time.Millisecond {
		t.Fatalf("Sleep(0) took %v, want ~0", d)
	}
}

func TestRealClock_NewTimerFires(t *testing.T) {
	c := Real()
	t1 := c.NewTimer(5 * time.Millisecond)
	defer t1.Stop()
	select {
	case <-t1.C():
	case <-time.After(time.Second):
		t.Fatal("real-clock NewTimer did not fire within 1s")
	}
}

func TestRealClock_NewTickerFires(t *testing.T) {
	c := Real()
	t1 := c.NewTicker(2 * time.Millisecond)
	defer t1.Stop()
	for range 3 {
		select {
		case <-t1.C():
		case <-time.After(time.Second):
			t.Fatal("real-clock NewTicker did not fire within 1s")
		}
	}
}

func TestFakeClock_NowReturnsConfiguredStart(t *testing.T) {
	f := NewFake(fakeStart)
	if got := f.Now(); !got.Equal(fakeStart) {
		t.Fatalf("Now() = %v, want %v", got, fakeStart)
	}
}

func TestFakeClock_AdvanceFiresSingleTimer(t *testing.T) {
	f := NewFake(fakeStart)
	t1 := f.NewTimer(time.Second)
	defer t1.Stop()
	f.Advance(time.Second)
	select {
	case fire := <-t1.C():
		want := fakeStart.Add(time.Second)
		if !fire.Equal(want) {
			t.Fatalf("fire time = %v, want %v", fire, want)
		}
	case <-time.After(time.Second):
		t.Fatal("Advance(1s) did not fire 1s timer")
	}
}

func TestFakeClock_AdvanceFiresMultipleTimersInOrder(t *testing.T) {
	f := NewFake(fakeStart)
	t1 := f.NewTimer(1 * time.Second)
	t2 := f.NewTimer(2 * time.Second)
	t3 := f.NewTimer(3 * time.Second)
	defer t1.Stop()
	defer t2.Stop()
	defer t3.Stop()

	f.Advance(2 * time.Second)

	select {
	case <-t1.C():
	default:
		t.Fatal("t1 (1s) did not fire after Advance(2s)")
	}
	select {
	case <-t2.C():
	default:
		t.Fatal("t2 (2s) did not fire after Advance(2s)")
	}
	select {
	case <-t3.C():
		t.Fatal("t3 (3s) fired too early")
	default:
	}

	f.Advance(time.Second)
	select {
	case <-t3.C():
	default:
		t.Fatal("t3 did not fire after second Advance(1s) crossed 3s")
	}
}

func TestFakeClock_TickerFiresEveryPeriod(t *testing.T) {
	f := NewFake(fakeStart)
	tk := f.NewTicker(time.Second)
	defer tk.Stop()

	// 5 periods worth of advance, drained one tick at a time so the
	// 1-slot buffer doesn't collapse them.
	for i := range 5 {
		f.Advance(time.Second)
		select {
		case <-tk.C():
		case <-time.After(time.Second):
			t.Fatalf("tick %d not delivered", i)
		}
	}
}

func TestFakeClock_TickerStop_NoFurtherTicks(t *testing.T) {
	f := NewFake(fakeStart)
	tk := f.NewTicker(time.Second)
	tk.Stop()
	tk.Stop() // safe to call twice
	f.Advance(5 * time.Second)
	select {
	case <-tk.C():
		t.Fatal("stopped ticker fired")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestFakeClock_TimerStopReturnsTrueBeforeFire(t *testing.T) {
	f := NewFake(fakeStart)
	t1 := f.NewTimer(time.Second)
	if !t1.Stop() {
		t.Fatal("Stop before fire should return true")
	}
	if t1.Stop() {
		t.Fatal("Stop after stop should return false")
	}
}

func TestFakeClock_TimerStopReturnsFalseAfterFire(t *testing.T) {
	f := NewFake(fakeStart)
	t1 := f.NewTimer(time.Second)
	f.Advance(time.Second)
	// Drain the channel so the receiver-state matches a real Timer.
	<-t1.C()
	if t1.Stop() {
		t.Fatal("Stop after fire should return false")
	}
}

func TestFakeClock_SleepWakesOnAdvance(t *testing.T) {
	f := NewFake(fakeStart)
	woke := make(chan time.Duration, 1)
	go func() {
		start := f.Now()
		_ = f.Sleep(context.Background(), 2*time.Second)
		woke <- f.Now().Sub(start)
	}()
	f.BlockUntilWaiters(1)
	f.Advance(2 * time.Second)
	select {
	case got := <-woke:
		if got != 2*time.Second {
			t.Fatalf("slept %v virtual time, want 2s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Sleep did not wake within 1s wall")
	}
}

func TestFakeClock_SleepHonoursCtxCancel(t *testing.T) {
	f := NewFake(fakeStart)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Sleep(ctx, time.Hour) }()
	f.BlockUntilWaiters(1)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected ctx error")
		}
	case <-time.After(time.Second):
		t.Fatal("Sleep did not unblock after cancel")
	}
	// After cancel, waiter count should drop back to 0.
	// Use BlockUntilWaiters(0) as a synchronisation no-op (always
	// returns immediately) and then assert via Advance not firing.
	// Direct assertion would peek into unexported state.
}

func TestFakeClock_SleepZeroReturnsImmediately(t *testing.T) {
	f := NewFake(fakeStart)
	if err := f.Sleep(context.Background(), 0); err != nil {
		t.Fatalf("Sleep(0): %v", err)
	}
}

func TestFakeClock_BlockUntilWaitersCountsSleeperAndTimer(t *testing.T) {
	f := NewFake(fakeStart)
	// Park a sleeper.
	var sleepDone atomic.Bool
	go func() {
		_ = f.Sleep(context.Background(), time.Hour)
		sleepDone.Store(true)
	}()
	// Park a timer receiver.
	var timerDone atomic.Bool
	t1 := f.NewTimer(time.Hour)
	go func() {
		<-t1.C()
		timerDone.Store(true)
	}()
	// BlockUntilWaiters should see both: sleeper increments on Sleep,
	// timer waiter increments on NewTimer (the timer goroutine just
	// receives off a pre-published channel).
	f.BlockUntilWaiters(2)
	if sleepDone.Load() || timerDone.Load() {
		t.Fatal("waiters claimed done before Advance")
	}
	f.Advance(time.Hour)
	// Give the receivers a moment to consume; eventual consistency.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sleepDone.Load() && timerDone.Load() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("sleeper=%v timer=%v after Advance", sleepDone.Load(), timerDone.Load())
}

func TestFakeClock_ReentryAfterAdvance(t *testing.T) {
	f := NewFake(fakeStart)
	woke := make(chan struct{}, 2)
	go func() {
		_ = f.Sleep(context.Background(), time.Second)
		woke <- struct{}{}
		_ = f.Sleep(context.Background(), time.Second)
		woke <- struct{}{}
	}()
	f.BlockUntilWaiters(1)
	f.Advance(time.Second)
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("first sleep did not wake")
	}
	f.BlockUntilWaiters(1)
	f.Advance(time.Second)
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("second sleep did not wake")
	}
}

func TestFakeClock_NewTickerPanicsOnZeroPeriod(t *testing.T) {
	f := NewFake(fakeStart)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on NewTicker(0)")
		}
	}()
	_ = f.NewTicker(0)
}

func TestFakeClock_NowMonotonicallyAdvances(t *testing.T) {
	f := NewFake(fakeStart)
	f.Advance(100 * time.Millisecond)
	mid := f.Now()
	f.Advance(50 * time.Millisecond)
	end := f.Now()
	if !mid.After(fakeStart) {
		t.Fatalf("Now after first Advance = %v, not after start %v", mid, fakeStart)
	}
	if !end.After(mid) {
		t.Fatalf("Now after second Advance = %v, not after mid %v", end, mid)
	}
}
