// Package normalize strips literal values from PostgreSQL queries and
// classifies the result into a privacy tier. It is the gateway through
// which every captured query passes before it can be transmitted off
// the collector — see docs/specs/2026-05-29-lynceus-design.md §2.
//
// Backed by libpg_query (the same parser Postgres uses), accessed via
// the pg_query_go cgo binding.
package normalize

import (
	"fmt"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Tier is the privacy classification of a piece of query-derived data.
type Tier int

const (
	// TierUnknown is the zero value and means "not yet classified".
	// It must never be transmitted.
	TierUnknown Tier = 0

	// TierNormalized indicates the text contains no literal values —
	// every literal has been replaced by a positional placeholder.
	// Safe to transmit as part of a T1 wire-contract message.
	TierNormalized Tier = 1

	// TierBlocked indicates the input could not be safely normalized
	// (parser rejected it). The text is dropped entirely; no part of
	// the original query is returned.
	TierBlocked Tier = 2
)

// Normalize replaces every literal in the query with a positional
// placeholder ($1, $2, …) and returns (normalized, TierNormalized) on
// success. If the parser cannot understand the input, it returns
// ("", TierBlocked) — the original text is discarded so that an
// unparseable, potentially literal-bearing string cannot escape.
func Normalize(query string) (string, Tier) {
	if query == "" {
		return "", TierBlocked
	}
	normalized, err := pg_query.Normalize(query)
	if err != nil {
		return "", TierBlocked
	}
	return normalized, TierNormalized
}

// Fingerprint returns a stable, hex-encoded hash of the query's
// parsed structure. Queries that differ only in literal values share
// a fingerprint; queries with different structure (different tables,
// different shape) do not.
func Fingerprint(query string) (string, error) {
	fp, err := pg_query.Fingerprint(query)
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	return fp, nil
}
