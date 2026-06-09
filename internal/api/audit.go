package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// dateLayout is the format produced by HTML <input type="date">.
const dateLayout = "2006-01-02"

// handleAuditPage renders the full filterable audit-log page.
func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	values, rows := s.fetchAudit(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditPage(values, rows).Render(r.Context(), w)
}

// handleAuditPartial renders just the results table, for HTMX in-place
// filtering.
func (s *Server) handleAuditPartial(w http.ResponseWriter, r *http.Request) {
	_, rows := s.fetchAudit(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditTable(rows).Render(r.Context(), w)
}

// fetchAudit parses the request's query params into a store filter,
// queries the config DB, and returns both the echoed-back form values
// and the rendered rows.
func (s *Server) fetchAudit(r *http.Request) (web.AuditFilterValues, []web.AuditRow) {
	q := r.URL.Query()
	values := web.AuditFilterValues{
		Actor:    q.Get("actor"),
		Action:   q.Get("action"),
		ServerID: q.Get("server"),
		Since:    q.Get("since"),
		Until:    q.Get("until"),
		Tier:     q.Get("tier"),
	}

	filter := store.AuditFilter{
		Actor:    values.Actor,
		Action:   values.Action,
		ServerID: values.ServerID,
		Limit:    200,
	}
	if t, err := time.Parse(dateLayout, values.Since); err == nil {
		filter.Since = t
	}
	if t, err := time.Parse(dateLayout, values.Until); err == nil {
		// Inclusive end-of-day so "until 2026-06-02" includes that day.
		filter.Until = t.Add(24*time.Hour - time.Nanosecond)
	}
	if n, err := strconv.Atoi(values.Tier); err == nil && (n == 1 || n == 2) {
		tier := int16(n)
		filter.Tier = &tier
	}

	recs, err := s.conf.ListAudit(r.Context(), filter)
	if err != nil {
		return values, nil
	}
	out := make([]web.AuditRow, 0, len(recs))
	for i := range recs {
		rec := &recs[i]
		out = append(out, web.AuditRow{
			ID:       rec.ID,
			Actor:    rec.Actor,
			Action:   rec.Action,
			ServerID: rec.ServerID,
			DataTier: rec.DataTier,
			Detail:   string(rec.Detail),
			At:       rec.At.UTC().Format(time.RFC3339),
		})
	}
	return values, out
}
