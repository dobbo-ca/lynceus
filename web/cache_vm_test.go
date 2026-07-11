package web

import "testing"

func TestSevClass(t *testing.T) {
	for in, want := range map[string]string{
		"crit": "c-sev-crit", "warn": "c-sev-warn", "info": "c-sev-info",
		"ok": "c-sev-ok", "": "c-sev-mut", "bogus": "c-sev-mut",
	} {
		if got := sevClass(in); got != want {
			t.Errorf("sevClass(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRoleAndAccessClass(t *testing.T) {
	if got := roleClass("PRIMARY"); got != "c-role c-role-primary" {
		t.Errorf("roleClass primary=%q", got)
	}
	if got := roleClass("REPLICA"); got != "c-role c-role-replica" {
		t.Errorf("roleClass replica=%q", got)
	}
	if got := accessClass("READ-WRITE"); got != "c-access c-access-rw" {
		t.Errorf("accessClass rw=%q", got)
	}
	if got := accessClass("READ-ONLY"); got != "c-access c-access-ro" {
		t.Errorf("accessClass ro=%q", got)
	}
}

func TestNextSort(t *testing.T) {
	if got := nextSort("health", "health", "name"); got != "name" {
		t.Errorf("nextSort from health=%q", got)
	}
	if got := nextSort("name", "health", "name"); got != "health" {
		t.Errorf("nextSort from name=%q", got)
	}
}
