package logparse

import (
	"reflect"
	"testing"
)

func TestPayloadTier(t *testing.T) {
	p := LogPayload{StatementText: "SELECT * FROM users WHERE email = 'a@b'"}
	if p.Tier() != TierSensitive {
		t.Fatalf("any non-empty payload field must be TierSensitive (T2); got %v", p.Tier())
	}
	empty := LogPayload{}
	if empty.Tier() != TierEmpty {
		t.Fatalf("empty payload must be TierEmpty; got %v", empty.Tier())
	}
}

func TestLogEventHasNoPayloadFields(t *testing.T) {
	// Sanity check at the Go-struct level: someone reading LogEvent's
	// definition must not be tempted to add a Statement / Detail / Hint
	// field. We assert by reflection that no field name overlaps
	// with the payload field set.
	forbidden := map[string]struct{}{
		"StatementText": {}, "Detail": {}, "Hint": {},
		"BindParameters": {}, "RawMessage": {}, "Query": {},
		"InternalQuery":  {}, "ErrorContext": {},
	}
	checkNoForbiddenFields(t, LogEvent{}, forbidden)
}

func checkNoForbiddenFields(t *testing.T, v any, forbidden map[string]struct{}) {
	t.Helper()
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if _, bad := forbidden[name]; bad {
			t.Fatalf("type %s has forbidden payload-bearing field %q — "+
				"T1 LogEvent must carry only classification metadata", rt.Name(), name)
		}
	}
}
