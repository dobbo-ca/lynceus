package console

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// StubExecutor returns a deterministic pg_stat_user_tables-shaped result
// (relname, n_dead_tup, last_autovacuum), truncated to Statement.RowLimit.
// It mirrors the design prototype's fixed dataset so the screen is fully
// demoable before the real transport lands.
type StubExecutor struct{}

var stubColumns = []string{"relname", "n_dead_tup", "last_autovacuum"}
var stubRows = buildStubRows()

// Execute ignores SQL and returns the fixed dataset truncated to RowLimit.
func (StubExecutor) Execute(_ context.Context, stmt Statement) (Result, error) {
	limit := stmt.RowLimit
	if limit <= 0 || limit > len(stubRows) {
		limit = len(stubRows)
	}
	rows := make([][]string, limit)
	copy(rows, stubRows[:limit])
	return Result{Columns: stubColumns, Rows: rows, DurationMs: 18.4}, nil
}

func buildStubRows() [][]string {
	base := []string{"orders", "orders_audit", "events", "customers", "order_items",
		"payments", "shipments", "refunds", "sessions", "webhooks"}
	seed := int64(7)
	rnd := func() int64 { seed = (seed * 16807) % 2147483647; return seed }
	var rows [][]string
	for i, tbl := range base {
		parts := 5
		if i < 4 {
			parts = 6
		}
		for p := 0; p < parts; p++ {
			name := tbl
			if p > 0 {
				name = fmt.Sprintf("%s_p2026_0%d", tbl, p)
			}
			ceil := int64(20000)
			if p == 0 {
				ceil = 200000
			}
			dead := rnd() % ceil
			var last string
			if rnd()%100 < 15 {
				last = "never"
			} else {
				last = fmt.Sprintf("2026-07-%02d %02d:%02d:12Z", 9+int(rnd()%2), rnd()%24, rnd()%60)
			}
			rows = append(rows, []string{name, groupThousands(dead), last})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool { return ungroup(rows[a][1]) > ungroup(rows[b][1]) })
	return rows
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	var out strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	return out.String()
}

func ungroup(s string) int64 {
	n, _ := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
	return n
}
