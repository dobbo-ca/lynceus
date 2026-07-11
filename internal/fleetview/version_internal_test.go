package fleetview

import "testing"

func TestFormatServerVersion(t *testing.T) {
	cases := map[string]string{"160003": "16.3", "150007": "15.7", "120000": "12.0", "": "", "garbage": ""}
	for in, want := range cases {
		if got := formatServerVersion(in); got != want {
			t.Errorf("formatServerVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
