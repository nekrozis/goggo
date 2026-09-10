package transfer

import (
	"context"
	"sync"
)

// schedule runs tasks through the worker fan-out and event delivery that both
// download paths share: an empty task list returns immediately with no events,
// the worker count is clamped to the task count, events flow through one
// channel to a single deliverer goroutine, and a cancelled context stops the
// dispatch and comes back as the run's error.
//
// The scheduler owns no failure semantics of its own. A task that fails emits
// its own failure events through emit and returns the error; a non-nil return
// only matters when the context is cancelled, which the scheduler surfaces as
// the run's error (review D65a and D67).
func schedule[T any](ctx context.Context, tasks []T, workers int, deliver func(Event), run func(context.Context, T, func(Event)) error) error {
	if len(tasks) == 0 {
		return nil
	}
	if workers <= 0 {
		workers = 1
	}
	if workers > len(tasks) {
		workers = len(tasks)
	}

	events := make(chan Event)
	deliverDone := make(chan struct{})
	go func() {
		defer close(deliverDone)
		for ev := range events {
			deliver(ev)
		}
	}()

	emit := func(ev Event) { events <- ev }

	queue := make(chan T)
	go func() {
		defer close(queue)
		for _, task := range tasks {
			select {
			case <-ctx.Done():
				return
			case queue <- task:
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case task, ok := <-queue:
					if !ok {
						return
					}
					if err := run(ctx, task, emit); err != nil && ctx.Err() != nil {
						return // cancelled: the run reports ctx.Err(), not per-task noise
					}
				}
			}
		}()
	}
	wg.Wait()
	close(events)
	<-deliverDone

	return ctx.Err()
}
