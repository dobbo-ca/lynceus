package advisor

import (
	"reflect"
	"testing"
)

func TestFilterColumns_equalityBeforeRange(t *testing.T) {
	got := filterColumns("((orders.status = $1) AND (orders.created_at > $2))")
	want := []string{"status", "created_at"} // equality first, then range
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterColumns = %v, want %v", got, want)
	}
}

func TestFilterColumns_dedupesAndStripsQualifier(t *testing.T) {
	got := filterColumns("(a.user_id = $1) AND (user_id = $2)")
	want := []string{"user_id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterColumns = %v, want %v", got, want)
	}
}

func TestFilterColumns_empty(t *testing.T) {
	if got := filterColumns(""); got != nil {
		t.Errorf("filterColumns(empty) = %v, want nil", got)
	}
}
