package api

import (
	"fmt"
	"math"
	"strings"

	"github.com/dobbo-ca/lynceus/internal/store"
)

// sparklinePoints converts QPS buckets into SVG <polyline> points over viewBox 0 0 100 24.
// Returns "" if fewer than 2 buckets (no meaningful sparkline). Still used by the
// cluster Overview screen (internal/api/overview.go).
func sparklinePoints(buckets []store.QPSBucket) string {
	if len(buckets) < 2 {
		return ""
	}
	n := len(buckets)
	minVal, maxVal := buckets[0].Calls, buckets[0].Calls
	for _, b := range buckets[1:] {
		if b.Calls < minVal {
			minVal = b.Calls
		}
		if b.Calls > maxVal {
			maxVal = b.Calls
		}
	}

	pts := make([]string, n)
	for i, b := range buckets {
		x := float64(i) * 100.0 / float64(n-1)
		var y float64
		if maxVal == minVal {
			y = 12
		} else {
			y = 22 - (float64(b.Calls-minVal)/float64(maxVal-minVal))*20
		}
		pts[i] = fmt.Sprintf("%.1f,%.1f", math.Round(x*10)/10, math.Round(y*10)/10)
	}
	return strings.Join(pts, " ")
}
