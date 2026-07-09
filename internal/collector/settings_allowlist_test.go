package collector

import (
	"strings"
	"testing"
)

// TestSettingsAllowlist_excludesFreeFormSettings is the LOAD-BEARING privacy
// test for the pg_settings reader. The curated allowlist must never intersect
// the set of GUCs whose value is free-form text capable of leaking infra
// detail or credentials (paths, hostnames, conninfo, log-line templates,
// key files), and the reader SQL must never be a wildcard SELECT * FROM
// pg_settings. A future edit adding a dangerous name — or switching to a
// wildcard — fails here.
func TestSettingsAllowlist_excludesFreeFormSettings(t *testing.T) {
	// Known free-form / infra-leaking GUCs. NONE may appear in the allowlist.
	denylist := []string{
		"log_line_prefix", "search_path", "application_name", "cluster_name",
		"archive_command", "restore_command", "primary_conninfo",
		"data_directory", "config_file", "hba_file", "ident_file",
		"log_directory", "log_filename", "stats_temp_directory",
		"unix_socket_directories", "ssl_cert_file", "ssl_key_file",
		"ssl_ca_file", "ssl_crl_file", "krb_server_keyfile", "syslog_ident",
	}

	allow := map[string]struct{}{}
	for _, n := range settingsAllowlist {
		if n == "*" {
			t.Fatalf("allowlist must be explicit names, found %q", n)
		}
		if _, dup := allow[n]; dup {
			t.Errorf("duplicate allowlist entry %q", n)
		}
		allow[n] = struct{}{}
	}
	for _, bad := range denylist {
		if _, ok := allow[bad]; ok {
			t.Errorf("allowlist contains free-form/leaking setting %q — remove it", bad)
		}
	}

	// The reader query must never be a wildcard select over pg_settings.
	if strings.Contains(settingsSQL, "*") {
		t.Errorf("settingsSQL must select explicit columns, never SELECT *: %q", settingsSQL)
	}
	if !strings.Contains(settingsSQL, "= ANY($1)") {
		t.Errorf("settingsSQL must filter by the allowlist via = ANY($1): %q", settingsSQL)
	}
}
