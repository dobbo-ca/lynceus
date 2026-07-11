package web

import (
	"strings"
	"testing"
)

func TestBuildAddComponentView_DatabaseSelf_realEnvContract(t *testing.T) {
	v := BuildAddComponentView(AddKindDatabase, ProviderSelf)

	if v.Title != "ADD DATABASE CLUSTER" {
		t.Errorf("Title = %q, want ADD DATABASE CLUSTER", v.Title)
	}
	if v.Noun != "CLUSTER" {
		t.Errorf("Noun = %q, want CLUSTER", v.Noun)
	}
	// The YAML must emit the REAL collector env contract (COMPARISON gap:
	// reconcile away the prototype TARGET_KIND/TARGET_ENDPOINT/LYNCEUS_TOKEN).
	for _, want := range []string{
		"LYNCEUS_SERVER_ID",
		"LYNCEUS_COLLECTOR_TOKEN",
		"LYNCEUS_INGESTION_URL",
		"LYNCEUS_PG_DSN",
		"kind: Deployment",
		"secretKeyRef",
	} {
		if !strings.Contains(v.YAML, want) {
			t.Errorf("YAML missing %q\n---\n%s", want, v.YAML)
		}
	}
	for _, bad := range []string{"TARGET_KIND", "TARGET_ENDPOINT", "LYNCEUS_TOKEN\n", "CLOUD_PROVIDER"} {
		if strings.Contains(v.YAML, bad) {
			t.Errorf("YAML still carries retired placeholder %q", bad)
		}
	}
	if v.ShowGuideLink {
		t.Error("self-hosted must not show the provider guide deep-link")
	}
	// chip selection reflects provider
	var selfSelected bool
	for _, c := range v.Chips {
		if c.ID == ProviderSelf {
			selfSelected = c.Selected
		}
	}
	if !selfSelected {
		t.Error("SELF-HOSTED chip must be marked Selected for ProviderSelf")
	}
}

func TestBuildAddComponentView_AWS_showsGuideAndRDSNote(t *testing.T) {
	v := BuildAddComponentView(AddKindDatabase, ProviderAWS)
	if !v.ShowGuideLink || v.GuideProvider != "aws" {
		t.Errorf("AWS must deep-link to provider guide aws; got ShowGuideLink=%v GuideProvider=%q", v.ShowGuideLink, v.GuideProvider)
	}
	for _, want := range []string{"AWS", "RDS", "IRSA"} {
		if !strings.Contains(v.ProviderNote, want) {
			t.Errorf("AWS ProviderNote missing %q: %q", want, v.ProviderNote)
		}
	}
}

func TestBuildAddComponentView_SearchDomainNoun(t *testing.T) {
	v := BuildAddComponentView(AddKindSearch, ProviderSelf)
	if v.Title != "ADD SEARCH DOMAIN" || v.Noun != "DOMAIN" {
		t.Errorf("search: Title=%q Noun=%q, want ADD SEARCH DOMAIN / DOMAIN", v.Title, v.Noun)
	}
}

func TestBuildAddComponentView_UnknownFallsBackToDatabaseSelf(t *testing.T) {
	v := BuildAddComponentView(AddComponentKind("bogus"), AddProvider("bogus"))
	if v.Title != "ADD DATABASE CLUSTER" || v.Provider != ProviderSelf {
		t.Errorf("fallback failed: Title=%q Provider=%q", v.Title, v.Provider)
	}
}
