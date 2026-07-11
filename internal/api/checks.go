package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleChecksPage(w http.ResponseWriter, r *http.Request) {
	sv := s.buildShellView(r, "checks")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksPage(sv, s.fetchChecks(r)).Render(r.Context(), w)
}

func (s *Server) handleChecksPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksTable(s.fetchChecks(r)).Render(r.Context(), w)
}

func (s *Server) fetchChecks(r *http.Request) web.ChecksVM {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -1)
	expand := r.URL.Query().Get("expand")
	nav := web.ScreenNav{Base: "/checks"} // fleet route; ly-ae6.3 refills under scope
	servers, err := s.stats.RecentServerIDs(r.Context(), since)
	if err != nil {
		return web.ChecksVM{}
	}
	var rows []web.ChecksRow
	cats := map[string]bool{}
	for _, srv := range servers {
		res, err := s.stats.LatestChecksResults(r.Context(), srv, since, now.Add(time.Minute))
		if err != nil {
			continue
		}
		// Overlay live mutes. LatestChecksResults.Muted only reflects mutes as of
		// the last scheduler run, but the MUTE toggle writes check_mutes directly
		// via SetMute — so a just-toggled mute must be read back from ListMutes for
		// the re-render to reflect it. Key by (check_id, object); an object=""
		// mute suppresses every object of that check.
		muted := map[string]bool{}
		checkWide := map[string]bool{}
		if ms, err := s.stats.ListMutes(r.Context(), srv); err == nil {
			for _, m := range ms {
				if m.Object == "" {
					checkWide[m.CheckID] = true
				} else {
					muted[m.CheckID+"\x00"+m.Object] = true
				}
			}
		}
		for i := range res {
			c := &res[i]
			// Only FIRING checks belong on this screen — filter before counting so
			// "N FIRING" is accurate and the "all healthy" empty state is reachable.
			if c.Status != "firing" {
				continue
			}
			isMuted := c.Muted || checkWide[c.CheckID] || muted[c.CheckID+"\x00"+c.Object]
			cats[strings.ToUpper(c.Category)] = true
			rows = append(rows, web.ChecksRow{
				Severity: c.Severity, Category: c.Category, CheckID: c.CheckID,
				Object: c.Object, Detail: c.Detail, ServerID: c.ServerID,
				FirstSeen: agoLabel(c.EvaluatedAt, now), Muted: isMuted,
				Expanded: c.CheckID == expand, Nav: nav,
			})
		}
	}
	return web.ChecksVM{Summary: checksSummary(len(rows), cats), Rows: rows}
}

// handleChecksMute toggles a mute for (check, object): if one exists it clears
// it, else it sets a 24h mute, then re-renders the table (fetchChecks overlays
// ListMutes so the button flips MUTE↔MUTED on the same round-trip).
func (s *Server) handleChecksMute(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	check := r.URL.Query().Get("check")
	object := r.URL.Query().Get("object")

	muted := false
	if ms, err := s.stats.ListMutes(r.Context(), server); err == nil {
		for _, m := range ms {
			if m.CheckID == check && m.Object == object {
				muted = true
				break
			}
		}
	}
	if muted {
		_ = s.stats.ClearMute(r.Context(), server, check, object)
	} else {
		_ = s.stats.SetMute(r.Context(), server, check, object, time.Now().Add(24*time.Hour), "muted from checks UI")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ChecksTable(s.fetchChecks(r)).Render(r.Context(), w)
}

// checksSummary builds the "N FIRING · CAT / CAT" header line.
func checksSummary(n int, cats map[string]bool) string {
	if n == 0 {
		return "0 FIRING"
	}
	var cs []string
	for c := range cats {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	return fmt.Sprintf("%d FIRING · %s", n, strings.Join(cs, " / "))
}
