// Package secure holds small, dependency-free guards that enforce
// encryption-in-transit at process startup. They validate connection
// strings/URLs before any connection is opened, so a misconfigured
// deployment fails fast and loudly rather than silently shipping data
// over plaintext. See docs/security/hitrust-controls.md (encryption in
// transit).
//
// Enforcement is gated by LYNCEUS_REQUIRE_TLS (default true in prod).
// Local dev against a plaintext Postgres sets LYNCEUS_REQUIRE_TLS=false.
package secure

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// sslModes that actually encrypt the Postgres connection. "prefer"/"allow"
// are excluded on purpose: they fall back to plaintext silently.
var encryptedSSLModes = map[string]bool{
	"require":     true,
	"verify-ca":   true,
	"verify-full": true,
}

// RequireTLS reports whether TLS enforcement is on. Defaults to true; set
// LYNCEUS_REQUIRE_TLS=false to opt out (local dev only).
func RequireTLS() bool {
	return !strings.EqualFold(os.Getenv("LYNCEUS_REQUIRE_TLS"), "false")
}

// CheckDatabaseDSN returns an error if requireTLS is set and dsn does not
// pin an encrypting sslmode (require/verify-ca/verify-full). It handles
// both URL DSNs (postgres://host/db?sslmode=require) and libpq keyword
// DSNs (host=... sslmode=require). An absent sslmode fails closed.
func CheckDatabaseDSN(dsn string, requireTLS bool) error {
	if !requireTLS {
		return nil
	}
	mode := sslModeOf(dsn)
	if !encryptedSSLModes[mode] {
		got := mode
		if got == "" {
			got = "<unset>"
		}
		return fmt.Errorf(
			"refusing insecure database connection: sslmode=%s — set sslmode to "+
				"require, verify-ca, or verify-full (verify-full recommended on RDS), "+
				"or set LYNCEUS_REQUIRE_TLS=false for local dev", got)
	}
	return nil
}

// CheckWebsocketURL returns an error if requireTLS is set and rawURL is not
// a wss:// (TLS) websocket URL. Used by the collector before dialing the
// ingestion server.
func CheckWebsocketURL(rawURL string, requireTLS bool) error {
	if !requireTLS {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid ingestion URL %q: %w", rawURL, err)
	}
	if !strings.EqualFold(u.Scheme, "wss") {
		return fmt.Errorf(
			"refusing insecure ingestion URL scheme %q — use wss:// (TLS), "+
				"or set LYNCEUS_REQUIRE_TLS=false for local dev", u.Scheme)
	}
	return nil
}

// sslModeOf extracts the sslmode from either DSN form, lower-cased. Returns
// "" if absent.
func sslModeOf(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			return strings.ToLower(u.Query().Get("sslmode"))
		}
		return ""
	}
	// libpq keyword form: scan for sslmode=<value> token.
	for _, field := range strings.Fields(dsn) {
		if k, v, ok := strings.Cut(field, "="); ok && strings.EqualFold(k, "sslmode") {
			return strings.ToLower(v)
		}
	}
	return ""
}
