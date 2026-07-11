package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dobbo-ca/lynceus/internal/store"
	"github.com/dobbo-ca/lynceus/web"
)

func (s *Server) handleWaitsPage(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	sv := s.shellViewFor(r, "waits")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.WaitsPage(sv, s.fetchWaits(r, server)).Render(r.Context(), w)
}

func (s *Server) handleWaitsPartial(w http.ResponseWriter, r *http.Request) {
	server := r.URL.Query().Get("server")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.WaitsView(s.fetchWaits(r, server)).Render(r.Context(), w)
}

func (s *Server) fetchWaits(r *http.Request, server string) web.WaitsVM {
	// ServerID is the poll key; ScopeLabel is the caption. They coincide today
	// (label defaults to the id) but ly-ae6.2 replaces ScopeLabel with a resolved
	// human label — keep ServerID separate so the ?server= poll never breaks.
	vm := web.WaitsVM{ServerID: server, ScopeLabel: server}
	if server == "" {
		return vm
	}
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -7)
	counts, err := s.stats.WaitEventHistogram(r.Context(), server, since, now)
	if err != nil || len(counts) == 0 {
		return vm
	}
	var total int64
	for _, c := range counts {
		total += c.Total
	}
	var mix []web.WaitMixSeg
	topClass, topColor := "", "var(--chart-cpu)"
	var topN int64
	for _, c := range counts {
		class := waitClass(c)
		color := waitColorVar(class)
		avg := int64(0)
		if c.Buckets > 0 {
			avg = c.Total / c.Buckets
		}
		vm.Legend = append(vm.Legend, web.WaitLegend{Key: class, Avg: fmt.Sprintf("%d", avg), ColorVar: color})
		width := 0
		if total > 0 {
			width = int(float64(c.Total) / float64(total) * 100)
		}
		mix = append(mix, web.WaitMixSeg{Key: class, WidthPct: width, ColorVar: color})
		if c.Total > topN {
			topN, topClass, topColor = c.Total, class, color
		}
	}
	vm.Servers = []web.WaitServerMix{{Name: server, TopClass: topClass, TopColorVar: topColor, Mix: mix}}
	return vm
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

// waitColorVar maps a wait class to its chart token color.
func waitColorVar(class string) string {
	switch {
	case class == "CPU":
		return "var(--chart-cpu)"
	case strings.HasPrefix(class, "IO"):
		return "var(--chart-io)"
	case strings.HasPrefix(class, "LWLock"):
		return "var(--chart-lwlock)"
	case strings.HasPrefix(class, "Lock"):
		return "var(--chart-lock)"
	case strings.HasPrefix(class, "Client"):
		return "var(--chart-client)"
	default:
		return "var(--chart-cpu)"
	}
}
