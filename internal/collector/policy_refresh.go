package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dobbo-ca/lynceus/internal/caps"
)

// policySnapshotEntry mirrors the api server's JSON shape
// (internal/api/capabilities.go).
type policySnapshotEntry struct {
	Capability   string `json:"capability"`
	DatabaseName string `json:"database_name"`
	Enabled      bool   `json:"enabled"`
}

// FetchPolicySnapshot GETs the effective capability policy for serverID
// from the api server and resolves it for the collector's own database
// db into a map[caps.GateKey]bool suitable for caps.Gate.Replace.
//
// Resolution mirrors store.EffectiveCapability: a db-specific override
// for db wins over the server-wide default ("" database_name). Entries
// scoped to a DIFFERENT database are ignored — the collector connects to
// one database and cannot honor another's per-db policy.
func FetchPolicySnapshot(ctx context.Context, baseURL, serverID, db string) (map[caps.GateKey]bool, error) {
	url := fmt.Sprintf("%s/api/servers/%s/policy-snapshot", baseURL, serverID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build policy-snapshot request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get policy-snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("policy-snapshot status %d", resp.StatusCode)
	}

	var entries []policySnapshotEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode policy-snapshot: %w", err)
	}

	// Two passes so an override always wins regardless of JSON order:
	// first apply server-wide defaults, then overlay this db's overrides.
	out := make(map[caps.GateKey]bool)
	for _, e := range entries {
		if e.DatabaseName == "" {
			out[caps.GateKey{Db: db, Cap: caps.Capability(e.Capability)}] = e.Enabled
		}
	}
	for _, e := range entries {
		if e.DatabaseName == db {
			out[caps.GateKey{Db: db, Cap: caps.Capability(e.Capability)}] = e.Enabled
		}
	}
	return out, nil
}
