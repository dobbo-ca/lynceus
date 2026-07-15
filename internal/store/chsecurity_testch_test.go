package store_test

import (
	"context"
	"testing"

	"github.com/dobbo-ca/lynceus/internal/testch"
)

// Pins that testch boots ClickHouse with access management enabled, so RBAC
// provisioning (CREATE USER / ROW POLICY) works in the store RBAC tests.
func TestCH_AccessManagementEnabled(t *testing.T) {
	ctx := context.Background()
	conn := testch.Start(t)
	if err := conn.Exec(ctx, "CREATE ROLE IF NOT EXISTS probe_role"); err != nil {
		t.Fatalf("access_management not enabled (CREATE ROLE failed): %v", err)
	}
	_ = conn.Exec(ctx, "DROP ROLE IF EXISTS probe_role")
}
