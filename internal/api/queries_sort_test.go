package api

import (
	"testing"

	"github.com/dobbo-ca/lynceus/web"
)

func rowsFixture() []web.TopQuery {
	return []web.TopQuery{
		{Fingerprint: "aaa", NormalizedQuery: "select * from orders", Calls: 10, TotalTimeMs: 100, MeanTimeMs: 10},
		{Fingerprint: "bbb", NormalizedQuery: "update users set x=$1", Calls: 5, TotalTimeMs: 200, MeanTimeMs: 40},
		{Fingerprint: "ccc", NormalizedQuery: "select * from items", Calls: 30, TotalTimeMs: 60, MeanTimeMs: 2},
	}
}

func TestSortAndFilter_SortByMeanDesc(t *testing.T) {
	s := &Server{}
	got := s.sortAndFilterQueries(rowsFixture(), web.QuerySort{Col: "mean", Dir: "desc"}, "")
	if got[0].Fingerprint != "bbb" || got[2].Fingerprint != "ccc" {
		t.Errorf("mean desc order wrong: %v", []string{got[0].Fingerprint, got[1].Fingerprint, got[2].Fingerprint})
	}
}

func TestSortAndFilter_FilterBySQLSubstring(t *testing.T) {
	s := &Server{}
	got := s.sortAndFilterQueries(rowsFixture(), web.QuerySort{Col: "total", Dir: "desc"}, "orders")
	if len(got) != 1 || got[0].Fingerprint != "aaa" {
		t.Errorf("filter should keep only the orders row, got %d rows", len(got))
	}
}

func TestSortAndFilter_FilterByFingerprint(t *testing.T) {
	s := &Server{}
	got := s.sortAndFilterQueries(rowsFixture(), web.QuerySort{Col: "calls", Dir: "asc"}, "ccc")
	if len(got) != 1 || got[0].Fingerprint != "ccc" {
		t.Errorf("fingerprint filter failed, got %d rows", len(got))
	}
}
