package console

import (
	"context"
	"time"
)

// SessionGrant is an active, time-boxed, per-cluster T2 grant. The real
// grant authority is the RBAC session-grant service (backend bead);
// StubGrantReader mirrors the prototype's single granted cluster.
type SessionGrant struct {
	ClusterName string
	Group       string
	Approver    string
	Incident    string
	ReadOnly    bool
	GrantedAt   time.Time
	ExpiresAt   time.Time
}

// GrantReader reports the caller's active session grant for a cluster.
type GrantReader interface {
	ActiveGrant(ctx context.Context, clusterID, clusterName, actor string) (SessionGrant, bool, error)
}

// StubGrantReader grants read-only access iff the cluster is named
// "orders-prod", with a fixed 3h12m window from Now.
type StubGrantReader struct{ Now func() time.Time }

// ActiveGrant implements GrantReader.
func (g StubGrantReader) ActiveGrant(_ context.Context, _, clusterName, _ string) (SessionGrant, bool, error) {
	if clusterName != "orders-prod" {
		return SessionGrant{}, false, nil
	}
	now := time.Now().UTC()
	if g.Now != nil {
		now = g.Now()
	}
	return SessionGrant{
		ClusterName: clusterName,
		Group:       "dba-oncall",
		Approver:    "j.alvarez",
		Incident:    "INC-2214",
		ReadOnly:    true,
		GrantedAt:   now,
		ExpiresAt:   now.Add(3*time.Hour + 12*time.Minute),
	}, true, nil
}
