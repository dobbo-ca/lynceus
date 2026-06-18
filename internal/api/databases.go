package api

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/fleetview"
	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// handleDatabases renders the full databases dashboard page.
func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	v := s.fetchDatabases(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesPage(v).Render(r.Context(), w)
}

// handleDatabasesPartial renders just the body fragment, for HTMX in-place filtering.
func (s *Server) handleDatabasesPartial(w http.ResponseWriter, r *http.Request) {
	v := s.fetchDatabases(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.DatabasesBody(v).Render(r.Context(), w)
}

// fetchDatabases parses the request query params, calls ListClusterSummaries,
// applies name filtering, and returns the DatabasesView.
func (s *Server) fetchDatabases(r *http.Request) web.DatabasesView {
	q := r.URL.Query()
	view := q.Get("view")
	if view != "list" {
		view = "cards"
	}
	query := q.Get("q")

	now := time.Now().UTC()
	sums, err := fleetview.ListClusterSummaries(r.Context(), s.conf, s.stats, now.AddDate(0, 0, -1), now)
	if err != nil {
		return web.DatabasesView{View: view, Query: query}
	}

	cards := make([]web.DatabaseCard, 0, len(sums))
	for i := range sums {
		sum := &sums[i]
		var qps float64
		if n := len(sum.QPSBuckets); n > 0 {
			qps = float64(sum.QPSBuckets[n-1].Calls) / 3600.0
		}
		card := web.DatabaseCard{
			ClusterID:     sum.Cluster.ID,
			Name:          sum.Cluster.Name,
			QPS:           qps,
			AvgLatencyMs:  sum.AvgLatencyMs,
			ActiveConns:   sum.ActiveConns,
			TopWait:       sum.TopWait,
			StreamCount:   sum.StreamCount,
			InstanceCount: sum.InstanceCount,
			InsightCount:  sum.InsightCount,
			Sparkline:     sparklinePoints(sum.QPSBuckets),
		}
		if query == "" || strings.Contains(strings.ToLower(card.Name), strings.ToLower(query)) {
			cards = append(cards, card)
		}
	}
	return web.DatabasesView{Cards: cards, View: view, Query: query}
}

// sparklinePoints converts QPS buckets into SVG <polyline> points over viewBox 0 0 100 24.
// Returns "" if fewer than 2 buckets (no meaningful sparkline).
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
