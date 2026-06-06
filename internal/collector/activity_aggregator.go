// internal/collector/activity_aggregator.go
package collector

import (
	"time"
)

// ActivitySample is one observation of pg_stat_activity for a single
// (database, state, wait_event_type, wait_event) tuple at one moment.
// It carries counts and labels only — never a query text or parameter.
type ActivitySample struct {
	Database      string
	State         string
	WaitEventType string
	WaitEvent     string
	Count         int64
}

// FlushedBucket is one aggregated bucket emitted by Flush. The collector
// converts these into proto ActivityBucket messages before shipping.
type FlushedBucket struct {
	ServerID      string
	Database      string
	State         string
	WaitEventType string
	WaitEvent     string
	BucketStart   time.Time
	BucketSeconds int32
	SampleCount   int32
	CountSum      int64
	CountMax      int64
}

// ActivityAggregator folds a stream of pg_stat_activity samples (one every
// ~10s) into fixed-width buckets (60s by default), keyed by (database,
// state, wait_event_type, wait_event). It is not safe for concurrent use —
// the collector calls Observe and Flush from a single goroutine.
type ActivityAggregator struct {
	serverID string
	width    time.Duration
	buckets  map[bucketKey]*FlushedBucket
}

type bucketKey struct {
	bucketStart   int64 // unix seconds
	database      string
	state         string
	waitEventType string
	waitEvent     string
}

// NewActivityAggregator returns an aggregator that emits buckets of the
// given width for serverID. width must be >= 1 second.
func NewActivityAggregator(serverID string, width time.Duration) *ActivityAggregator {
	if width < time.Second {
		width = time.Second
	}
	return &ActivityAggregator{
		serverID: serverID,
		width:    width,
		buckets:  make(map[bucketKey]*FlushedBucket),
	}
}

// Observe folds the samples taken at sampleTime into the appropriate bucket
// for each (database, state, wait_event_type, wait_event) tuple.
func (a *ActivityAggregator) Observe(sampleTime time.Time, samples []ActivitySample) {
	bucketStart := sampleTime.UTC().Truncate(a.width)
	for _, s := range samples {
		k := bucketKey{
			bucketStart:   bucketStart.Unix(),
			database:      s.Database,
			state:         s.State,
			waitEventType: s.WaitEventType,
			waitEvent:     s.WaitEvent,
		}
		b, ok := a.buckets[k]
		if !ok {
			b = &FlushedBucket{
				ServerID:      a.serverID,
				Database:      s.Database,
				State:         s.State,
				WaitEventType: s.WaitEventType,
				WaitEvent:     s.WaitEvent,
				BucketStart:   bucketStart,
				BucketSeconds: int32(a.width / time.Second),
			}
			a.buckets[k] = b
		}
		b.SampleCount++
		b.CountSum += s.Count
		if s.Count > b.CountMax {
			b.CountMax = s.Count
		}
	}
}

// Flush returns every accumulated bucket and resets internal state. Buckets
// are returned in unspecified order — callers should not rely on ordering.
func (a *ActivityAggregator) Flush() []FlushedBucket {
	out := make([]FlushedBucket, 0, len(a.buckets))
	for _, b := range a.buckets {
		out = append(out, *b)
	}
	a.buckets = make(map[bucketKey]*FlushedBucket)
	return out
}
