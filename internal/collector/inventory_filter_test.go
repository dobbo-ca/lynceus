package collector_test

import (
	"testing"

	"github.com/dobbo-ca/lynceus/internal/collector"
)

func TestSchemaFilter_DefaultAllowsAll(t *testing.T) {
	f, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	for _, s := range []string{"public", "patient_phi", "internal", ""} {
		if !f.IsAllowed(s) {
			t.Errorf("default filter rejected %q", s)
		}
	}
}

func TestSchemaFilter_IgnoreExcludesMatch(t *testing.T) {
	f, err := collector.NewSchemaFilter("", "^patient_.*")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	if f.IsAllowed("patient_phi") {
		t.Error("expected patient_phi to be excluded by ignore_schema_regexp")
	}
	if !f.IsAllowed("public") {
		t.Error("expected public to be allowed")
	}
}

func TestSchemaFilter_IncludeIsAllowlist(t *testing.T) {
	f, err := collector.NewSchemaFilter("^(public|reporting)$", "")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	if !f.IsAllowed("public") {
		t.Error("expected public to be allowed")
	}
	if !f.IsAllowed("reporting") {
		t.Error("expected reporting to be allowed")
	}
	if f.IsAllowed("patient_phi") {
		t.Error("expected patient_phi to be rejected (not on allowlist)")
	}
}

// Ignore wins over include — a schema explicitly excluded must not be
// rescued by being on the include list. This makes the regex pair
// safe to combine (operator can allowlist "*" then carve out PHI).
func TestSchemaFilter_IgnoreWinsOverInclude(t *testing.T) {
	f, err := collector.NewSchemaFilter(".*", "^patient_.*")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	if f.IsAllowed("patient_phi") {
		t.Error("ignore_schema_regexp must override include_schema_regexp")
	}
	if !f.IsAllowed("public") {
		t.Error("public must remain allowed")
	}
}

// Postgres system schemas are always ignored — Lynceus never inventories them.
func TestSchemaFilter_AlwaysSkipsSystemSchemas(t *testing.T) {
	f, err := collector.NewSchemaFilter("", "")
	if err != nil {
		t.Fatalf("NewSchemaFilter: %v", err)
	}
	for _, s := range []string{"pg_catalog", "information_schema", "pg_toast"} {
		if f.IsAllowed(s) {
			t.Errorf("system schema %q must be rejected unconditionally", s)
		}
	}
}

func TestSchemaFilter_InvalidRegexpErrors(t *testing.T) {
	if _, err := collector.NewSchemaFilter("[", ""); err == nil {
		t.Error("expected error for invalid include regexp")
	}
	if _, err := collector.NewSchemaFilter("", "["); err == nil {
		t.Error("expected error for invalid ignore regexp")
	}
}
