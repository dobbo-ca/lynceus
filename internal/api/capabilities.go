package api

import (
	"encoding/json"
	"net/http"

	"github.com/dobbo-ca/lynceus/internal/caps"
	"github.com/dobbo-ca/lynceus/internal/store"
)

// capabilityCellDTO is one row of the capability matrix: the discovered
// availability of a capability crossed with operator policy and the
// resulting final-enabled decision. Every field is an enum capability
// string, a bounded package-authored reason, a boolean, or an
// operator-supplied database identifier — no monitored-DB literal.
type capabilityCellDTO struct {
	Capability       string `json:"capability"`
	DatabaseName     string `json:"database_name"`     // "" = server-wide
	DiscoveredAvail  bool   `json:"discovered_available"`
	DiscoveredReason string `json:"discovered_reason"` // bounded, package-authored
	PolicyEnabled    *bool  `json:"policy_enabled"`    // nil = no explicit policy row
	PolicySource     string `json:"policy_source"`     // "server-default"|"database-override"|""
	FinalEnabled     bool   `json:"final_enabled"`     // discovered && effective(default-enabled)
}

type capabilityMatrixDTO struct {
	ServerID string              `json:"server_id"`
	Cells    []capabilityCellDTO `json:"cells"`
}

// actorFromContext returns the principal to attribute audited writes to.
// Real OIDC actor wiring is the Milestone-5 follow-up; under DevAuth this
// is the constant dev-admin stub.
func actorFromContext(_ *http.Request) string { return "dev-admin" }

// handleCapabilityMatrix returns the discovered × policy × final-enabled
// matrix for one server. The capability axis is caps.Declared() so every
// declared capability appears even with no discovery or policy row.
// Absent policy => enabled (the effective-policy default).
func (s *Server) handleCapabilityMatrix(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	ctx := r.Context()

	discovered, err := s.disc.ListDiscoveredCapabilities(ctx, serverID)
	if err != nil {
		http.Error(w, "failed to load discovered capabilities", http.StatusInternalServerError)
		return
	}
	// Index the server-level discovered row (database_name == "") per
	// capability; the matrix here reports the server-level cell.
	discByCap := make(map[string]store.DiscoveredCapability, len(discovered))
	for _, d := range discovered {
		if d.DatabaseName == "" {
			discByCap[d.Capability] = d
		}
	}

	out := capabilityMatrixDTO{ServerID: serverID}
	for _, c := range caps.Declared() {
		capStr := string(c)
		cell := capabilityCellDTO{Capability: capStr}
		if d, ok := discByCap[capStr]; ok {
			cell.DiscoveredAvail = d.Available
			cell.DiscoveredReason = d.Reason
		}

		enabled, source, found, err := s.conf.EffectiveCapability(ctx, serverID, "", capStr)
		if err != nil {
			http.Error(w, "failed to resolve effective policy", http.StatusInternalServerError)
			return
		}
		effective := true // absent policy => enabled (ly-xnk.3 default)
		if found {
			eCopy := enabled
			cell.PolicyEnabled = &eCopy
			cell.PolicySource = string(source)
			effective = enabled
		}

		cell.FinalEnabled = cell.DiscoveredAvail && effective
		out.Cells = append(out.Cells, cell)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type toggleRequestDTO struct {
	DatabaseName string `json:"database"` // "" => server-wide default
	Enabled      bool   `json:"enabled"`
	Reason       string `json:"reason"`
}

// handleCapabilityToggle sets one capability_policy row for a server (or a
// database within it) and records a tamper-evident audit entry, reusing
// store.SetCapabilityPolicy (which appends the audit row first, then
// upserts the policy carrying its audit_chain_id).
func (s *Server) handleCapabilityToggle(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	capability := r.PathValue("cap")

	if !isDeclaredCapability(capability) {
		http.Error(w, "unknown capability", http.StatusBadRequest)
		return
	}

	var req toggleRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	got, err := s.conf.SetCapabilityPolicy(r.Context(), store.SetCapabilityPolicyInput{
		ServerID:     serverID,
		DatabaseName: req.DatabaseName,
		Capability:   capability,
		Enabled:      req.Enabled,
		SetBy:        actorFromContext(r),
		Reason:       req.Reason,
	})
	if err != nil {
		http.Error(w, "failed to set capability policy", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ServerID     string `json:"server_id"`
		DatabaseName string `json:"database_name"`
		Capability   string `json:"capability"`
		Enabled      bool   `json:"enabled"`
		AuditChainID int64  `json:"audit_chain_id"`
	}{
		ServerID:     got.ServerID,
		DatabaseName: got.DatabaseName,
		Capability:   got.Capability,
		Enabled:      got.Enabled,
		AuditChainID: got.AuditChainID,
	})
}

// isDeclaredCapability reports whether capability is one caps.Declared()
// knows about — rejecting typos before they create a policy row.
func isDeclaredCapability(capability string) bool {
	for _, c := range caps.Declared() {
		if string(c) == capability {
			return true
		}
	}
	return false
}
