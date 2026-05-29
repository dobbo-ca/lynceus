package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// topQueryDTO is the on-the-wire shape of a top-queries result. It
// is intentionally tiny and snake-cased — see future API design.
type topQueryDTO struct {
	Fingerprint     string  `json:"fingerprint"`
	NormalizedQuery string  `json:"normalized_query"`
	Calls           int64   `json:"calls"`
	TotalTimeMs     float64 `json:"total_time_ms"`
}

func (s *Server) handleTopQueries(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	// Default window: last 30 days. Future: parse since/until.
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)

	rows, err := s.stats.TopQueriesByTotalTime(r.Context(), since, now, limit)
	if err != nil {
		http.Error(w, "stats query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out := make([]topQueryDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, topQueryDTO{
			Fingerprint:     r.Fingerprint,
			NormalizedQuery: r.NormalizedQuery,
			Calls:           r.Calls,
			TotalTimeMs:     r.TotalTimeMs,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
