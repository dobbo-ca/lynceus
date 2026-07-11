package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

// dateLayout is the format produced by HTML <input type="date">.
const dateLayout = "2006-01-02"

// handleAuditPage renders the full filterable audit-log page inside the design
// shell.
func (s *Server) handleAuditPage(w http.ResponseWriter, r *http.Request) {
	values, rows := s.fetchAudit(r)
	chain := s.auditChain(r.Context())
	sv := s.buildShellView(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditPage(sv, chain, values, rows).Render(r.Context(), w)
}

// handleAuditPartial renders just the results grid, for HTMX in-place filtering.
func (s *Server) handleAuditPartial(w http.ResponseWriter, r *http.Request) {
	_, rows := s.fetchAudit(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.AuditTable(rows).Render(r.Context(), w)
}

// auditChain computes the page-level tamper-evidence banner: whole-chain
// verification plus the current tip (hash + id-as-count, valid because audit
// ids are contiguous from 1).
func (s *Server) auditChain(ctx context.Context) web.AuditChain {
	idx, _, err := s.conf.VerifyChain(ctx, time.Time{}, time.Time{})
	ch := web.AuditChain{Verified: err == nil && idx == -1, TipShort: "—"}
	tip, err := s.conf.ListAudit(ctx, store.AuditFilter{Limit: 1})
	if err == nil && len(tip) > 0 {
		ch.TipShort = hashShort(tip[0].RowHash)
		ch.Count = tip[0].ID
	}
	return ch
}

// fetchAudit parses the request's query params into a store filter, queries the
// config DB, and returns both the echoed-back form values and the rendered rows.
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
			ID:        rec.ID,
			Actor:     rec.Actor,
			Action:    rec.Action,
			ServerID:  rec.ServerID,
			DataTier:  rec.DataTier,
			Detail:    string(rec.Detail),
			At:        rec.At.UTC().Format(time.RFC3339),
			Target:    auditTarget(rec.Detail),
			HashShort: hashShort(rec.RowHash),
			IsT2:      rec.DataTier == 2,
		})
	}
	return values, out
}

// auditTarget builds the acted-upon object label from the audit Detail's
// closed-vocabulary structural keys. It never surfaces a monitored-DB literal.
func auditTarget(detail []byte) string {
	if len(detail) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(detail, &m); err != nil {
		return ""
	}
	db, _ := m["database_name"].(string)
	capName, _ := m["capability"].(string)
	switch {
	case db != "" && capName != "":
		return db + "." + capName
	case capName != "":
		return capName
	case db != "":
		return db
	default:
		return ""
	}
}

// hashShort renders a 32-byte chain hash as "abcd…f01" (first4…last3 hex).
func hashShort(h []byte) string {
	if len(h) == 0 {
		return "—"
	}
	s := hex.EncodeToString(h)
	if len(s) < 7 {
		return s
	}
	return s[:4] + "…" + s[len(s)-3:]
}
