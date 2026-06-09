package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleWaitsPage(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.WaitsPage(server, s.fetchWaits(r, server)).Render(r.Context(), w)
}

func (s *Server) handleWaitsPartial(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.WaitsTable(s.fetchWaits(r, server)).Render(r.Context(), w)
}

func (s *Server) fetchWaits(r *http.Request, server string) []web.WaitRow {
	if server == "" {
		return nil
	}
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -7) // 7-day wait window
	counts, err := s.stats.WaitEventHistogram(r.Context(), server, since, now)
	if err != nil {
		return nil
	}
	var total int64
	for _, c := range counts {
		total += c.Total
	}
	var out []web.WaitRow
	for _, c := range counts {
		out = append(out, web.WaitRow{
			Class:   waitClass(c),
			Total:   c.Total,
			Buckets: c.Buckets,
			Pct:     pct(c.Total, total),
		})
	}
	return out
}

func waitClass(c store.WaitEventCount) string {
	if c.WaitEventType == "" && c.WaitEvent == "" {
		return "CPU"
	}
	if c.WaitEvent == "" {
		return c.WaitEventType
	}
	return c.WaitEventType + " / " + c.WaitEvent
}

func pct(n, total int64) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", float64(n)/float64(total)*100)
}
