package scope_test

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/scope"
)

func TestEncodeParse_roundTrip(t *testing.T) {
	cases := []scope.Scope{
		{Kind: scope.Cluster, ClusterID: "c-1"},
		{Kind: scope.Node, ClusterID: "c-1", NodeID: "n-1"},
		{Kind: scope.Pooler, PoolerID: "p-1"},
		{Kind: scope.Database, ClusterID: "c-1", Database: "orders"},
		{Kind: scope.Database, ClusterID: "c-1", Database: "weird:name"}, // colon in db name survives
	}
	for _, sc := range cases {
		got := scope.Parse(sc.Encode())
		if got != sc {
			t.Errorf("round-trip %+v -> %q -> %+v", sc, sc.Encode(), got)
		}
	}
}

func TestEncode_fleetIsEmpty(t *testing.T) {
	if enc := (scope.Scope{Kind: scope.Fleet}).Encode(); enc != "" {
		t.Errorf("fleet Encode() = %q, want empty", enc)
	}
	if enc := (scope.Scope{}).Encode(); enc != "" {
		t.Errorf("zero-value Encode() = %q, want empty", enc)
	}
}

func TestParse_emptyAndUnknownAreFleet(t *testing.T) {
	for _, raw := range []string{"", "garbage", "cluster", "db:only-one-part"} {
		if !scope.Parse(raw).IsFleet() {
			t.Errorf("Parse(%q) should be fleet", raw)
		}
	}
}

func TestIsFleet(t *testing.T) {
	if !(scope.Scope{}).IsFleet() {
		t.Error("zero value must be fleet")
	}
	if !(scope.Scope{Kind: scope.Fleet}).IsFleet() {
		t.Error("explicit fleet must be fleet")
	}
	if (scope.Scope{Kind: scope.Cluster, ClusterID: "x"}).IsFleet() {
		t.Error("cluster is not fleet")
	}
}
