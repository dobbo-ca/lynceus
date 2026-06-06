package store

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

func TestCanonicalJSON_sortsKeysAndStripsWhitespace(t *testing.T) {
	in := []byte(`{ "b": 2,  "a": [1, 2,  3], "c": {"y": 1, "x": 2} }`)
	got, err := canonicalJSON(in)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	want := `{"a":[1,2,3],"b":2,"c":{"x":2,"y":1}}`
	if string(got) != want {
		t.Fatalf("canonicalJSON = %q, want %q", got, want)
	}
}

func TestHashAuditRow_isStableForSameInputs(t *testing.T) {
	at := time.Date(2026, 5, 29, 10, 0, 0, 123456789, time.UTC)
	prev := make([]byte, 32) // genesis prev
	h1 := hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{"fingerprint":"abc"}`), at)
	h2 := hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{"fingerprint":"abc"}`), at)
	if !bytes.Equal(h1, h2) {
		t.Fatalf("hash not deterministic")
	}
	if len(h1) != 32 {
		t.Fatalf("hash length = %d, want 32", len(h1))
	}
}

func TestHashAuditRow_changesWhenAnyFieldChanges(t *testing.T) {
	at := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	prev := make([]byte, 32)
	base := hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{}`), at)

	mutations := []struct {
		name string
		got  []byte
	}{
		{"id", hashAuditRow(2, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{}`), at)},
		{"prev", hashAuditRow(1, bytes.Repeat([]byte{1}, 32), "alice", "viewed.t2", "srv-1", 2, []byte(`{}`), at)},
		{"actor", hashAuditRow(1, prev, "bob", "viewed.t2", "srv-1", 2, []byte(`{}`), at)},
		{"action", hashAuditRow(1, prev, "alice", "viewed.t1", "srv-1", 2, []byte(`{}`), at)},
		{"server", hashAuditRow(1, prev, "alice", "viewed.t2", "srv-2", 2, []byte(`{}`), at)},
		{"tier", hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 1, []byte(`{}`), at)},
		{"detail", hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{"x":1}`), at)},
		{"at", hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{}`), at.Add(time.Nanosecond))},
	}
	for _, m := range mutations {
		if bytes.Equal(base, m.got) {
			t.Errorf("changing %s did not change the hash", m.name)
		}
	}
}

func TestHashAuditRow_goldenVector(t *testing.T) {
	// Pins the canonical byte layout against regression. If you change the
	// layout intentionally, regenerate this vector and bump hashDomain to v2.
	at := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	prev := make([]byte, 32)
	h := hashAuditRow(1, prev, "alice", "viewed.t2", "srv-1", 2, []byte(`{"fingerprint":"abc"}`), at)
	want := "88c8ba1782a30c8d85eaa17f032c465d849543935cc738790197b702c421e53b"
	if hex.EncodeToString(h) != want {
		t.Logf("hash = %s", hex.EncodeToString(h))
		t.Fatalf("golden vector mismatch")
	}
}

func TestGenesisPrevIsZero(t *testing.T) {
	if len(genesisPrev) != 32 {
		t.Fatalf("genesisPrev len = %d, want 32", len(genesisPrev))
	}
	for _, b := range genesisPrev {
		if b != 0 {
			t.Fatal("genesisPrev must be all zero")
		}
	}
}
