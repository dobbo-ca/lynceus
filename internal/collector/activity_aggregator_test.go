// internal/collector/activity_aggregator_test.go
package collector_test

import (
	"testing"
	"time"

	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestAggregator_foldsSamplesIntoOneBucketPerKey(t *testing.T) {
	a := collector.NewActivityAggregator("srv-1", 60*time.Second)

	// Bucket start: 2026-05-27 12:34:00 UTC.
	t0 := time.Date(2026, 5, 27, 12, 34, 5, 0, time.UTC)
	t1 := t0.Add(10 * time.Second)
	t2 := t0.Add(20 * time.Second)

	a.Observe(t0, []collector.ActivitySample{
		{Database: "app", State: "active", Count: 3},
		{Database: "app", State: "idle", Count: 7},
	})
	a.Observe(t1, []collector.ActivitySample{
		{Database: "app", State: "active", Count: 5},
		{Database: "app", State: "idle", Count: 6},
	})
	a.Observe(t2, []collector.ActivitySample{
		{Database: "app", State: "active", Count: 4},
		{Database: "app", State: "idle", Count: 9},
	})

	buckets := a.Flush()
	if len(buckets) != 2 {
		t.Fatalf("want 2 buckets (active, idle), got %d", len(buckets))
	}

	byState := map[string]*collector.FlushedBucket{}
	for i := range buckets {
		byState[buckets[i].State] = &buckets[i]
	}

	active := byState["active"]
	if active == nil {
		t.Fatal("missing active bucket")
	}
	if active.SampleCount != 3 {
		t.Errorf("active.SampleCount = %d, want 3", active.SampleCount)
	}
	if active.CountSum != 12 { // 3+5+4
		t.Errorf("active.CountSum = %d, want 12", active.CountSum)
	}
	if active.CountMax != 5 {
		t.Errorf("active.CountMax = %d, want 5", active.CountMax)
	}
	wantStart := time.Date(2026, 5, 27, 12, 34, 0, 0, time.UTC)
	if !active.BucketStart.Equal(wantStart) {
		t.Errorf("active.BucketStart = %s, want %s", active.BucketStart, wantStart)
	}

	idle := byState["idle"]
	if idle.CountSum != 22 || idle.CountMax != 9 || idle.SampleCount != 3 {
		t.Errorf("idle bucket = %+v, want sum=22 max=9 samples=3", idle)
	}
}

func TestAggregator_flushClearsState(t *testing.T) {
	a := collector.NewActivityAggregator("srv-1", 60*time.Second)
	ts := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	a.Observe(ts, []collector.ActivitySample{{Database: "app", State: "active", Count: 1}})

	if len(a.Flush()) != 1 {
		t.Fatal("first flush should emit one bucket")
	}
	if got := a.Flush(); len(got) != 0 {
		t.Errorf("second flush should be empty, got %d", len(got))
	}
}

func TestAggregator_rollsOverWhenSampleCrossesBoundary(t *testing.T) {
	a := collector.NewActivityAggregator("srv-1", 60*time.Second)

	a.Observe(
		time.Date(2026, 5, 27, 12, 0, 50, 0, time.UTC),
		[]collector.ActivitySample{{Database: "app", State: "active", Count: 2}},
	)
	a.Observe(
		time.Date(2026, 5, 27, 12, 1, 5, 0, time.UTC), // next minute
		[]collector.ActivitySample{{Database: "app", State: "active", Count: 3}},
	)

	buckets := a.Flush()
	if len(buckets) != 2 {
		t.Fatalf("want 2 buckets (one per minute), got %d", len(buckets))
	}
}
