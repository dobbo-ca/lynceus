package console

import (
	"strings"
	"testing"
)

func sampleResult() Result {
	return Result{
		Columns: []string{"relname", "n_dead_tup", "last_autovacuum"},
		Rows:    [][]string{{"orders", "182,431", "never"}, {"events, live", "12", "2026-07-10 01:02:12Z"}},
	}
}

func TestCopyTSV_underGuard(t *testing.T) {
	tsv, tooLarge := CopyTSV(sampleResult())
	if tooLarge {
		t.Fatal("small result should not be too large")
	}
	if !strings.HasPrefix(tsv, "relname\tn_dead_tup\tlast_autovacuum\n") {
		t.Errorf("tsv header wrong: %q", tsv)
	}
	if !strings.Contains(tsv, "orders\t182,431\tnever") {
		t.Errorf("tsv body wrong: %q", tsv)
	}
}

func TestCopyTSV_overGuardReturnsTooLarge(t *testing.T) {
	big := Result{Columns: []string{"c"}}
	for i := 0; i < ConsoleCopyGuardBytes/4+10; i++ {
		big.Rows = append(big.Rows, []string{"xxxx"})
	}
	tsv, tooLarge := CopyTSV(big)
	if !tooLarge || tsv != "" {
		t.Errorf("over-guard should be ('', true), got (%d bytes, %v)", len(tsv), tooLarge)
	}
}

func TestCSV_quotesFieldsWithCommas(t *testing.T) {
	csv := CSV(sampleResult())
	if !strings.Contains(csv, `"events, live"`) {
		t.Errorf("comma field not quoted: %q", csv)
	}
}

func TestSQLInserts_emitsInsertPerRow(t *testing.T) {
	sql := SQLInserts(sampleResult(), "result")
	if strings.Count(sql, "INSERT INTO result") != 2 {
		t.Errorf("want 2 INSERTs, got: %q", sql)
	}
}
