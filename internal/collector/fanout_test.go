// Unit tests for the bounded fan-out primitive. Pure Go, no DB: they exercise
// the concurrency bound (the per-cycle query budget that keeps the collector
// gentle on the monitored Postgres), prove the pool is not accidentally serial,
// and prove one failing task never aborts or cancels its siblings.
package collector_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestRunBounded_neverExceedsBudget(t *testing.T) {
	const budget = 3
	const n = 8
	var inFlight, maxSeen atomic.Int64
	ran := make([]atomic.Bool, n)

	tasks := make([]collector.Task, n)
	for i := range tasks {
		i := i
		tasks[i] = collector.Task{
			Name: "task",
			Run: func(context.Context) error {
				cur := inFlight.Add(1)
				for {
					m := maxSeen.Load()
					if cur <= m || maxSeen.CompareAndSwap(m, cur) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				inFlight.Add(-1)
				ran[i].Store(true)
				return nil
			},
		}
	}

	errs := collector.RunBounded(context.Background(), budget, tasks)

	if got := maxSeen.Load(); got > budget {
		t.Fatalf("peak concurrency %d exceeded budget %d", got, budget)
	}
	for i := range ran {
		if !ran[i].Load() {
			t.Fatalf("task %d did not run", i)
		}
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("errs[%d] = %v, want nil", i, err)
		}
	}
}

func TestRunBounded_reachesBudget(t *testing.T) {
	const budget = 3
	arrived := make(chan struct{}, budget+2)
	release := make(chan struct{})

	tasks := make([]collector.Task, budget+2)
	for i := range tasks {
		tasks[i] = collector.Task{
			Name: "task",
			Run: func(context.Context) error {
				arrived <- struct{}{}
				<-release
				return nil
			},
		}
	}

	done := make(chan []error, 1)
	go func() { done <- collector.RunBounded(context.Background(), budget, tasks) }()

	// Exactly `budget` tasks must run concurrently: read budget arrivals within
	// a timeout. A serial pool would deliver only 1 and time out here.
	deadline := time.After(2 * time.Second)
	for i := 0; i < budget; i++ {
		select {
		case <-arrived:
		case <-deadline:
			t.Fatalf("only %d tasks arrived, want %d — pool is serial", i, budget)
		}
	}
	close(release)

	select {
	case errs := <-done:
		for i, err := range errs {
			if err != nil {
				t.Fatalf("errs[%d] = %v, want nil", i, err)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunBounded did not return after release")
	}
}

func TestRunBounded_serialWhenBudgetOne(t *testing.T) {
	const n = 4
	var inFlight, maxSeen atomic.Int64

	newTasks := func() []collector.Task {
		tasks := make([]collector.Task, n)
		for i := range tasks {
			tasks[i] = collector.Task{
				Name: "task",
				Run: func(context.Context) error {
					cur := inFlight.Add(1)
					for {
						m := maxSeen.Load()
						if cur <= m || maxSeen.CompareAndSwap(m, cur) {
							break
						}
					}
					time.Sleep(5 * time.Millisecond)
					inFlight.Add(-1)
					return nil
				},
			}
		}
		return tasks
	}

	collector.RunBounded(context.Background(), 1, newTasks())
	if got := maxSeen.Load(); got != 1 {
		t.Fatalf("budget=1 peak concurrency %d, want exactly 1", got)
	}

	// budget < 1 must clamp to 1 (no panic, no unbounded fan-out).
	maxSeen.Store(0)
	collector.RunBounded(context.Background(), 0, newTasks())
	if got := maxSeen.Load(); got != 1 {
		t.Fatalf("budget=0 peak concurrency %d, want clamped to 1", got)
	}
}

func TestRunBounded_oneErrorDoesNotSinkOthers(t *testing.T) {
	const n = 5
	boom := errors.New("boom")
	var mu sync.Mutex
	results := make([]int, n)

	tasks := make([]collector.Task, n)
	for i := range tasks {
		i := i
		tasks[i] = collector.Task{
			Name: "task",
			Run: func(context.Context) error {
				mu.Lock()
				results[i] = i + 1
				mu.Unlock()
				if i == 2 {
					return boom
				}
				return nil
			},
		}
	}

	errs := collector.RunBounded(context.Background(), 3, tasks)

	if !errors.Is(errs[2], boom) {
		t.Fatalf("errs[2] = %v, want boom", errs[2])
	}
	for i, err := range errs {
		if i == 2 {
			continue
		}
		if err != nil {
			t.Fatalf("errs[%d] = %v, want nil", i, err)
		}
	}
	for i := range results {
		if results[i] != i+1 {
			t.Fatalf("results[%d] = %d, want %d — sibling did not run", i, results[i], i+1)
		}
	}
}
