package web

import "strings"

// ConsoleVM is the whole SQL Console screen. It is the app's single explicitly
// T2 surface: statement text (Editor.SQL) and result cells (Result.Rows) are
// literal-capable and rendered ONLY inside the granted console body.
type ConsoleVM struct {
	Shell            ShellView // top bar + sidebar wrapper (page render only)
	ClusterID        string
	ClusterName      string
	Granted          bool
	Grant            ConsoleGrantVM
	Picker           ConsolePickerVM
	CapabilitiesHref string // request-access target shown when locked
	Editor           ConsoleEditorVM
	HasResult        bool
	Result           ConsoleResultVM
	History          []ConsoleHistoryRow
}

// ConsoleGrantVM is the active session-grant banner content.
type ConsoleGrantVM struct {
	Group     string
	Incident  string
	Expires   string
	Approver  string
	AuditHref string
	ReadOnly  bool
}

// ConsolePickerVM is the target picker (cluster header + node/database axes).
type ConsolePickerVM struct {
	ClusterLabel  string
	GrantChip     string
	Granted       bool
	Nodes         []ConsoleChip
	Databases     []ConsoleChip
	NodeFixed     bool
	DatabaseFixed bool
}

// ConsoleChip is one selectable target axis value. An empty Href renders inert
// (a fixed axis).
type ConsoleChip struct {
	Label    string
	Href     string
	Selected bool
}

// ConsoleEditorVM is the SQL editor toolbar + textarea state.
type ConsoleEditorVM struct {
	TargetName        string
	SQL               string // T2: restored/last statement text
	Node              string // resolved target node (hidden form input)
	Database          string // resolved target database (hidden form input)
	RowLimit          int
	TimeoutSecs       int
	Ready             bool
	RunHref           string
	SaveScriptsHref   string // seam → ly-ae6.9
	SearchScriptsHref string // seam → ly-ae6.9
}

// ConsoleResultVM is the current result page + pagination/export controls. Rows
// are the current page only; literal-capable (T2 surface).
type ConsoleResultVM struct {
	Columns      []string
	Rows         [][]string
	TotalRows    int
	DurationMs   float64
	Hash         string // short display form "6c1d…e44"
	PageLabel    string
	PrevHref     string
	NextHref     string
	PrevActive   bool
	NextActive   bool
	PageSizes    []ConsolePageSize
	CopyTSV      string // full-result TSV for client copy; "" if too large
	CopyTooLarge bool
	CsvHref      string
	SqlHref      string
}

// ConsolePageSize is one rows-per-page chip.
type ConsolePageSize struct {
	Label    string
	Href     string
	Selected bool
}

// ConsoleHistoryRow is one strict-audit statement-history entry (click to
// restore its result).
type ConsoleHistoryRow struct {
	TS   string
	Stmt string
	Ms   string
	Hash string
	Href string
}

// boolAttr renders a boolean HTML attribute value ("1" | "0").
func boolAttr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// upper uppercases a display label (column headers).
func upper(s string) string { return strings.ToUpper(s) }
