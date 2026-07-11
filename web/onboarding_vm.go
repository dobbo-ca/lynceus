package web

import "strings"

// AddComponentKind identifies which vertical's "+ ADD" wizard is open.
type AddComponentKind string

const (
	AddKindDatabase AddComponentKind = "database"
	AddKindSearch   AddComponentKind = "search"
	AddKindCache    AddComponentKind = "cache"
)

// AddProvider identifies the selected provider chip in the wizard.
type AddProvider string

const (
	ProviderSelf  AddProvider = "self"
	ProviderAWS   AddProvider = "aws"
	ProviderAzure AddProvider = "azure"
)

// ProviderChip is one selectable provider option in the wizard header.
type ProviderChip struct {
	ID       AddProvider
	Label    string
	Selected bool
}

// AddComponentView is the full view-model for the + ADD wizard modal.
// All fields are static guidance or generated manifests with placeholder
// values only — no database data.
type AddComponentView struct {
	Kind          AddComponentKind
	Title         string
	Noun          string
	Provider      AddProvider
	Chips         []ProviderChip
	YAML          string
	ProviderNote  string
	ShowGuideLink bool
	GuideProvider string
}

type addKindMeta struct {
	title string // "ADD DATABASE CLUSTER"
	noun  string // "CLUSTER" / "DOMAIN"
	dep   string // deployment name suffix
	dsn   string // LYNCEUS_PG_DSN placeholder
}

// addKinds. NOTE on the `dsn` field for search/cache: the DATABASE kind maps
// cleanly onto the real collector var LYNCEUS_PG_DSN (a Postgres DSN — the only
// endpoint var cmd/collector/main.go actually reads). The search/cache rows put
// an OpenSearch/Valkey URL in the SAME LYNCEUS_PG_DSN slot, which is
// semantically wrong for a Postgres-named var — those engines need their own
// endpoint env var, which the real collector does not yet define. This is
// harmless HERE because only the DATABASE "+ ADD" entry point is wired (Task 6);
// the search/cache entry points are deferred to their own vertical beads. Those
// beads MUST replace this placeholder DSN mapping with the correct per-engine
// endpoint var before wiring a search/cache "+ ADD" button — do not inherit this
// misleading manifest.
var addKinds = map[AddComponentKind]addKindMeta{
	AddKindDatabase: {title: "ADD DATABASE CLUSTER", noun: "CLUSTER", dep: "postgres", dsn: "postgres://$(LYNCEUS_DB_ROLE)@<primary-host>:5432/postgres"},
	AddKindSearch:   {title: "ADD SEARCH DOMAIN", noun: "DOMAIN", dep: "opensearch", dsn: "https://<domain-endpoint>:9200"}, // placeholder slot — see NOTE above
	AddKindCache:    {title: "ADD CACHE CLUSTER", noun: "CLUSTER", dep: "valkey", dsn: "valkey://<primary-host>:6379"},      // placeholder slot — see NOTE above
}

// BuildAddComponentView assembles the wizard view-model for a vertical +
// provider selection. Unknown kind falls back to database; unknown provider
// falls back to self-hosted.
func BuildAddComponentView(kind AddComponentKind, provider AddProvider) AddComponentView {
	meta, ok := addKinds[kind]
	if !ok {
		kind, meta = AddKindDatabase, addKinds[AddKindDatabase]
	}
	switch provider {
	case ProviderSelf, ProviderAWS, ProviderAzure:
	default:
		provider = ProviderSelf
	}

	v := AddComponentView{
		Kind:     kind,
		Title:    meta.title,
		Noun:     meta.noun,
		Provider: provider,
		YAML:     addYAML(meta),
		Chips: []ProviderChip{
			{ID: ProviderSelf, Label: "SELF-HOSTED", Selected: provider == ProviderSelf},
			{ID: ProviderAWS, Label: "AWS", Selected: provider == ProviderAWS},
			{ID: ProviderAzure, Label: "AZURE", Selected: provider == ProviderAzure},
		},
	}
	switch provider {
	case ProviderAWS:
		v.ProviderNote = "3 · AWS — attach an IRSA role scoped strictly to RDS (rds:Describe* on lynceus=true-tagged ARNs; CloudWatch reads limited to the AWS/RDS namespace) so the collector can read provider metadata and cover endpoints it cannot query directly, like a Multi-AZ standby."
		v.ShowGuideLink = true
		v.GuideProvider = "aws"
	case ProviderAzure:
		v.ProviderNote = "3 · AZURE — grant the collector identity Monitoring Reader on the resource group so it can pull Azure Monitor metrics (covers the zone-redundant HA standby)."
		v.ShowGuideLink = true
		v.GuideProvider = "azure"
	default:
		v.ProviderNote = "3 · SELF-HOSTED — the collector connects straight to the endpoint in LYNCEUS_PG_DSN; no cloud role required."
	}
	return v
}

// addYAML renders the copyable Kubernetes Deployment using the real collector
// env contract (LYNCEUS_SERVER_ID / LYNCEUS_COLLECTOR_TOKEN /
// LYNCEUS_INGESTION_URL / LYNCEUS_PG_DSN — see cmd/collector/main.go).
func addYAML(m addKindMeta) string {
	lines := []string{
		"apiVersion: apps/v1",
		"kind: Deployment",
		"metadata:",
		"  name: lynceus-collector-" + m.dep,
		"  namespace: lynceus",
		"spec:",
		"  replicas: 1",
		"  selector:",
		"    matchLabels: { app: lynceus-collector-" + m.dep + " }",
		"  template:",
		"    metadata:",
		"      labels: { app: lynceus-collector-" + m.dep + " }",
		"    spec:",
		"      serviceAccountName: lynceus-collector",
		"      containers:",
		"        - name: collector",
		"          image: lynceus/collector:1.8",
		"          env:",
		"            - name: LYNCEUS_SERVER_ID",
		"              value: \"<cluster-name>\"",
		"            - name: LYNCEUS_COLLECTOR_TOKEN",
		"              valueFrom: { secretKeyRef: { name: lynceus-token, key: token } }",
		"            - name: LYNCEUS_INGESTION_URL",
		"              value: \"wss://ingest.<region>.lynceus.io/v1/collector\"",
		"            - name: LYNCEUS_PG_DSN",
		"              value: \"" + m.dsn + "\"",
	}
	return strings.Join(lines, "\n")
}
