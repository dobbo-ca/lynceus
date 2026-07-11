package web

import (
	"context"
	"strings"
	"testing"
)

func TestDatabasesListBody_QualifiedIdentityAndInfoStrip(t *testing.T) {
	var sb strings.Builder
	v := DatabasesListView{
		CountLabel: "2 DATABASES ACROSS 1 CLUSTER", Sort: "name", SortLabel: "NAME",
		Groups: []DatabaseGroupVM{{
			Name: "orders-prod", EngineIcon: "eng-pg", EngineName: "POSTGRES",
			Version: "16.3", HealthText: "[DEGRADED] 1 CRIT · 4 WARN", HealthClass: "hl-crit",
			ScopeHref: "/?scope=cluster%3Ac1",
			Entries: []DatabaseEntryVM{{
				Name: "orders", Qual: "orders-prod/orders", Size: "—", QPS: "1,102",
				Conns: "64", Cache: "—", Tables: "—",
				ScopeHref: "/?scope=db%3Ac1%3Aorders",
			}},
		}},
	}
	if err := DatabasesListBody(v).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{
		`id="databases-screen"`,
		`2 DATABASES ACROSS 1 CLUSTER`,
		`A DATABASE IS IDENTIFIED BY CLUSTER + NAME`, // info strip
		`class="info-strip"`,
		`orders-prod/orders`, // qualified identity sub-line
		`class="db-name"`, `>orders<`,
		`1,102`, `href="/?scope=db%3Ac1%3Aorders"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("DatabasesListBody missing %q", want)
		}
	}
}
