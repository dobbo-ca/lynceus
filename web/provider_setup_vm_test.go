package web

import (
	"strings"
	"testing"
)

func TestBuildProviderSetupView_Unselected(t *testing.T) {
	v := BuildProviderSetupView("")
	if v.Selected != "" || v.Guide != nil {
		t.Errorf("unselected: Selected=%q Guide=%v, want empty/nil", v.Selected, v.Guide)
	}
	if len(v.Blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(v.Blocks))
	}
	wantMarks := map[ProviderID]string{ProviderSetupAWS: "AWS", ProviderSetupAzure: "AZ", ProviderSetupPlanetScale: "PS"}
	for _, b := range v.Blocks {
		if wantMarks[b.ID] != b.Mark {
			t.Errorf("block %q mark = %q, want %q", b.ID, b.Mark, wantMarks[b.ID])
		}
		if b.Selected {
			t.Errorf("no block should be selected in the unselected state (%q)", b.ID)
		}
	}
}

func TestBuildProviderSetupView_AWS_threePathsAndTerraform(t *testing.T) {
	v := BuildProviderSetupView(ProviderSetupAWS)
	if v.Guide == nil {
		t.Fatal("aws guide is nil")
	}
	if len(v.Guide.Steps) != 6 {
		t.Fatalf("aws steps = %d, want 6", len(v.Guide.Steps))
	}
	// selected block flag
	var awsSel bool
	for _, b := range v.Blocks {
		if b.ID == ProviderSetupAWS {
			awsSel = b.Selected
		}
	}
	if !awsSel {
		t.Error("AWS block must be marked Selected")
	}
	titles := []string{
		"PATH 1 — DIRECT AGENT CONNECTION",
		"PATH 2 — RESOURCE API ACCESS (IAM, RDS-ONLY)",
		"PATH 3 — FIREHOSE INGESTION (CONTROLLED INGRESS)",
		"QUERYSET MAPPING",
		"TERRAFORM",
		"VERIFY",
	}
	for i, want := range titles {
		if v.Guide.Steps[i].Title != want {
			t.Errorf("step[%d].Title = %q, want %q", i, v.Guide.Steps[i].Title, want)
		}
	}
	joined := v.Guide.Intro
	for _, s := range v.Guide.Steps {
		joined += "\n" + s.Body + "\n" + s.Code
	}
	for _, want := range []string{
		"LYNCEUS_DB_ROLE",                      // path 1 env-placeholder role
		"pg_monitor",                           // required tier
		"pg_signal_backend",                    // maintenance tier
		`"aws:ResourceTag/lynceus": "true"`,    // path 2 RDS scoping
		`"cloudwatch:namespace": "AWS/RDS"`,    // path 2 namespace scope
		"Firehose",                             // path 3 controlled ingress
		"X-Lynceus-Tenant",                     // path 3 tenant header
		"aws_kinesis_firehose_delivery_stream", // terraform
		"aws_cloudwatch_metric_stream",         // terraform metric stream
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("aws guide missing %q", want)
		}
	}
	// VERIFY step has no code block
	if v.Guide.Steps[5].Code != "" {
		t.Error("VERIFY step must have no code block")
	}
}

func TestBuildProviderSetupView_AzureAndPlanetScale(t *testing.T) {
	az := BuildProviderSetupView(ProviderSetupAzure)
	if az.Guide == nil || len(az.Guide.Steps) != 4 {
		t.Fatalf("azure steps = %v", az.Guide)
	}
	if !strings.Contains(az.Guide.Steps[0].Code, "Monitoring Reader") {
		t.Error("azure step 1 must grant Monitoring Reader")
	}
	ps := BuildProviderSetupView(ProviderSetupPlanetScale)
	if ps.Guide == nil || len(ps.Guide.Steps) != 4 {
		t.Fatalf("planetscale steps = %v", ps.Guide)
	}
	if !strings.Contains(ps.Guide.Steps[1].Code, "http_sd") {
		t.Error("planetscale step 2 must use http_sd service discovery")
	}
}

func TestBuildProviderSetupView_UnknownIsUnselected(t *testing.T) {
	v := BuildProviderSetupView(ProviderID("gcp"))
	if v.Selected != "" || v.Guide != nil {
		t.Error("unknown provider must render as unselected")
	}
}
