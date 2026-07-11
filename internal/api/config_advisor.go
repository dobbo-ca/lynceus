package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dobbo-ca/lynceus/internal/advisor"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleConfigAdvisorPage(w http.ResponseWriter, r *http.Request) {
	sv := s.shellViewFor(r, "configadvisor")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConfigAdvisorPage(sv, s.fetchConfigAdvice(r)).Render(r.Context(), w)
}

func (s *Server) handleConfigAdvisorPartial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.ConfigAdvisorTable(s.fetchConfigAdvice(r)).Render(r.Context(), w)
}

// fetchConfigAdvice discovers servers seen in the last 24h (via RecentServerIDs
// — settings exist without stored plans), runs ConfigAdvice per server, and
// selects the ?server= tab (default: first). Errors degrade to an empty VM.
func (s *Server) fetchConfigAdvice(r *http.Request) web.ConfigAdvisorVM {
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	vm := web.ConfigAdvisorVM{Nav: web.ScreenNav{Base: "/config-advisor"}} // fleet route; ly-ae6.3 refills
	servers, err := s.stats.RecentServerIDs(r.Context(), since)
	if err != nil || len(servers) == 0 {
		return vm
	}
	sel := r.URL.Query().Get("server")
	if sel == "" {
		sel = servers[0]
	}
	for _, srv := range servers {
		var in []advisor.ConfigSettingInput
		if rows, err := s.stats.LatestSettings(r.Context(), srv, now); err == nil {
			for i := range rows {
				in = append(in, advisor.ConfigSettingInput{
					Name: rows[i].Name, Value: rows[i].Value, Unit: rows[i].Unit, Source: rows[i].Source,
				})
			}
		}
		recs := advisor.ConfigAdvice(in)
		vm.Servers = append(vm.Servers, web.ConfigServerTab{
			ID: srv, Label: srv, Sub: fmt.Sprintf("%d findings", len(recs)), Selected: srv == sel,
		})
		if srv == sel {
			vm.ScopeName = srv + " · CONFIG"
			for _, rec := range recs {
				vm.Rows = append(vm.Rows, web.ConfigAdvisorRow{
					Group: string(rec.Category), Setting: rec.Setting, SevClass: web.SevClass(string(rec.Severity)),
					Current: rec.Current, Suggested: rec.Suggested, Detail: rec.Detail,
				})
			}
		}
	}
	return vm
}
