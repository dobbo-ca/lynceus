package collector

import (
	"context"
	"sync"
)

// Task is one named unit of monitored-DB work in a collection cycle.
type Task struct {
	Name string
	Run  func(context.Context) error
}

// RunBounded runs every Task concurrently but never more than `budget` at
// once — the global per-cycle query budget that keeps the collector gentle on
// the monitored Postgres. It returns after all Tasks finish; errs[i] is
// tasks[i].Run's error (nil on success). A failing Task never aborts or
// cancels a sibling: the parent ctx is passed straight through (deliberately
// NOT errgroup, which cancels on first error). budget < 1 is clamped to 1.
func RunBounded(ctx context.Context, budget int, tasks []Task) []error {
	if budget < 1 {
		budget = 1
	}
	errs := make([]error, len(tasks))
	sem := make(chan struct{}, budget)
	var wg sync.WaitGroup
	for i := range tasks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// errs[i] is written by exactly one goroutine and read only
			// after wg.Wait(), so there is no data race.
			errs[i] = tasks[i].Run(ctx)
		}(i)
	}
	wg.Wait()
	return errs
}
