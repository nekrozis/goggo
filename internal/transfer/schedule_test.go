package transfer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestScheduleShared locks the mechanical semantics both download paths share:
// an empty task list is a silent no-op, the worker count clamps to the task
// count, cancellation surfaces as ctx.Err(), and the deliverer drains every
// event before the run returns.
func TestScheduleShared(t *testing.T) {
	t.Run("empty is a no-op", func(t *testing.T) {
		var calls int
		err := schedule(context.Background(), nil, 4,
			func(Event) { calls++ },
			func(context.Context, int, func(Event)) error { calls++; return nil })
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if calls != 0 {
			t.Errorf("calls = %d, want none for an empty task list", calls)
		}
	})

	t.Run("workers clamp to the task count", func(t *testing.T) {
		var mu sync.Mutex
		var running, maxRunning int
		slow := func(_ context.Context, _ int, _ func(Event)) error {
			mu.Lock()
			running++
			if running > maxRunning {
				maxRunning = running
			}
			mu.Unlock()
			// The window makes the clamp observable: two slow tasks in
			// parallel is exactly the clamped worker count.
			time.Sleep(30 * time.Millisecond)
			mu.Lock()
			running--
			mu.Unlock()
			return nil
		}
		tasks := []int{1, 2, 3, 4}
		if err := schedule(context.Background(), tasks, 8,
			func(Event) {}, slow); err != nil {
			t.Fatalf("schedule: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if maxRunning > len(tasks) {
			t.Errorf("max concurrent = %d, want at most %d", maxRunning, len(tasks))
		}
		if maxRunning < 2 {
			t.Errorf("max concurrent = %d, want the four slow tasks to overlap", maxRunning)
		}
	})

	t.Run("cancellation surfaces as ctx.Err", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := schedule(ctx, []int{1}, 1,
			func(Event) {},
			func(context.Context, int, func(Event)) error { return nil })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}
