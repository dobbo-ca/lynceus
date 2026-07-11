// Package console holds the SQL-Console execution/grant/session seams.
// It is the app's single explicitly-T2 execution surface: statement text
// and result cells are literal-capable and permitted here alone, behind a
// session grant, with every run audited by the caller.
package console

import "context"

// Result is one executed statement's full result set. Cell values are
// already-rendered display strings; literals are allowed (T2 surface only).
type Result struct {
	Columns    []string
	Rows       [][]string
	DurationMs float64
}

// RowCount is the total number of rows in the full result (not a page).
func (r Result) RowCount() int { return len(r.Rows) }

// Statement is one bounded, read-only execution request against a single
// resolved (cluster, node, database) target.
type Statement struct {
	ClusterID   string
	ClusterName string
	Node        string
	Database    string
	ServerID    string
	SQL         string
	RowLimit    int
	TimeoutSecs int
	Actor       string
}

// Executor runs one bounded read-only statement and returns its rows. The
// production implementation is a net-new outbound execution transport
// (tracked as a separate backend bead); StubExecutor stands in until then.
type Executor interface {
	Execute(ctx context.Context, stmt Statement) (Result, error)
}
