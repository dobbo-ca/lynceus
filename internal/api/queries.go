package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

// sortAndFilterQueries applies a case-insensitive substring filter over
// fingerprint+normalized SQL, then a stable sort by the chosen column.
// Default sort is total-time descending (matches the store's own order).
func (s *Server) sortAndFilterQueries(rows []web.TopQuery, q web.QuerySort, filter string) []web.TopQuery {
	out := rows[:0:0]
	f := strings.ToLower(strings.TrimSpace(filter))
	for _, r := range rows {
		if f == "" || strings.Contains(strings.ToLower(r.Fingerprint), f) ||
			strings.Contains(strings.ToLower(r.NormalizedQuery), f) {
			out = append(out, r)
		}
	}
	less := func(i, j int) bool {
		a, b := out[i], out[j]
		switch q.Col {
		case "calls":
			return a.Calls < b.Calls
		case "mean":
			return a.MeanTimeMs < b.MeanTimeMs
		case "rows":
			return a.Rows < b.Rows
		case "hit":
			return a.CacheHitPct < b.CacheHitPct
		default: // "total"
			return a.TotalTimeMs < b.TotalTimeMs
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if q.Dir == "asc" {
			return less(i, j)
		}
		return less(j, i)
	})
	return out
}

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
