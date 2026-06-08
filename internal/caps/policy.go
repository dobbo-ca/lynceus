package caps

import "context"

// PolicyResolver is the minimal interface that Allowed needs. It is
// satisfied by *store.Config via a thin adapter (see policy_test.go and
// internal/api/capabilities.go) because store imports caps and we cannot
// create a direct dependency in the other direction.
type PolicyResolver interface {
	// EffectiveCapabilityEnabled reports whether a capability is enabled
	// for (serverID, databaseName). found is false when no policy row
	// exists (the caller decides the absent-policy default).
	EffectiveCapabilityEnabled(ctx context.Context, serverID, databaseName, capability string) (enabled bool, found bool, err error)
}

// Allowed resolves whether a capability is enabled for (serverID, db)
// using ly-xnk.3 default-enabled semantics:
//
//	per-db override  »  server-wide default  »  ENABLED (fail-open)
//
// Absent policy returns true so a freshly-provisioned server collects
// until an operator deliberately disables a capability.
func Allowed(ctx context.Context, r PolicyResolver, serverID, db string, c Capability) (bool, error) {
	enabled, found, err := r.EffectiveCapabilityEnabled(ctx, serverID, db, string(c))
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil // absent policy => enabled
	}
	return enabled, nil
}
