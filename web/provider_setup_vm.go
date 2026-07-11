package web

// ProviderID identifies a provider setup guide.
type ProviderID string

const (
	ProviderSetupAWS         ProviderID = "aws"
	ProviderSetupAzure       ProviderID = "azure"
	ProviderSetupPlanetScale ProviderID = "planetscale"
)

// ProviderBlock is one big block-button in the provider chooser.
type ProviderBlock struct {
	ID       ProviderID
	Label    string
	Mark     string
	Sub      string
	Selected bool
}

// GuideStep is one numbered step in a provider guide.
type GuideStep struct {
	N     string
	Title string
	Body  string
	Code  string
}

// ProviderGuide is a full provider setup guide.
type ProviderGuide struct {
	Intro string
	Steps []GuideStep
}

// ProviderSetupView is the full Provider Setup page view-model.
type ProviderSetupView struct {
	Blocks   []ProviderBlock
	Selected ProviderID
	Guide    *ProviderGuide
}

// BuildProviderSetupView returns the page view-model for the given selection.
// An empty or unknown selection yields the unselected state (Guide == nil).
func BuildProviderSetupView(selected ProviderID) ProviderSetupView {
	guide, ok := providerGuides[selected]
	if !ok {
		selected = ""
	}
	v := ProviderSetupView{
		Selected: selected,
		Blocks: []ProviderBlock{
			{ID: ProviderSetupAWS, Label: "AWS", Mark: "AWS", Sub: "CloudWatch", Selected: selected == ProviderSetupAWS},
			{ID: ProviderSetupAzure, Label: "Azure", Mark: "AZ", Sub: "Azure Monitor", Selected: selected == ProviderSetupAzure},
			{ID: ProviderSetupPlanetScale, Label: "PlanetScale", Mark: "PS", Sub: "Prometheus", Selected: selected == ProviderSetupPlanetScale},
		},
	}
	if selected != "" {
		g := guide
		v.Guide = &g
	}
	return v
}

var providerGuides = map[ProviderID]ProviderGuide{
	ProviderSetupAWS: {
		Intro: "Three data paths work together on AWS. Path 1: the agent (running in your Kubernetes cluster) connects directly to the database / search / cache endpoint and runs queries against it. Path 2: an IAM role lets the Lynceus role call the resource APIs for metadata (instance class, parameter groups, tags) — not logs or metrics. Path 3: CloudWatch ships metrics and logs about the resource back to Lynceus over a push pipeline.",
		Steps: []GuideStep{
			{N: "1", Title: "PATH 1 — DIRECT AGENT CONNECTION",
				Body: "The agent connects to the endpoint like any client and runs queries directly (pg_stat_*, cluster health APIs, INFO). The role name is never hardcoded — it comes from LYNCEUS_DB_ROLE so each environment can set its own. Grants are tiered: baseline is read-only monitoring; each optional tier unlocks more capabilities (visible on the Capabilities screen), up to owner-level.",
				Code: `# role name is an environment placeholder — set per environment
#   env: LYNCEUS_DB_ROLE=lynceus_monitor        (staging)
#   env: LYNCEUS_DB_ROLE=lynceus_monitor_prod   (production)

-- REQUIRED · baseline monitoring (read-only stats)
CREATE ROLE :"LYNCEUS_DB_ROLE" LOGIN PASSWORD :'LYNCEUS_DB_PASSWORD';
GRANT pg_monitor TO :"LYNCEUS_DB_ROLE";

-- OPTIONAL · extensions — enable trusted extensions on PG 13+
--   (pg_stat_statements, auto_explain)
GRANT CREATE ON DATABASE <db> TO :"LYNCEUS_DB_ROLE";

-- OPTIONAL · maintenance — cancel/terminate runaway backends
GRANT pg_signal_backend TO :"LYNCEUS_DB_ROLE";

-- OPTIONAL · owner-level — full control of the target database
--   (apply index advisor DDL, schema changes); grant deliberately
ALTER DATABASE <db> OWNER TO :"LYNCEUS_DB_ROLE";

# collector env — same deployment as the + ADD wizard.
# The real collector reads LYNCEUS_PG_DSN (see cmd/collector/main.go) — the
# wizard emits the identical var, so the two surfaces agree.
env:
  - { name: LYNCEUS_DB_ROLE,     value: "<role>" }      # per environment
  - { name: LYNCEUS_DB_PASSWORD, valueFrom: { secretKeyRef: { name: lynceus-db, key: password } } }
  - { name: LYNCEUS_PG_DSN,      value: "postgres://$(LYNCEUS_DB_ROLE)@<primary-host>:5432/postgres" }
  # SHIP_VIA / FIREHOSE_STREAM below are illustrative pipeline hints only — NOT
  # read by the collector; controlled ingress is configured Terraform-side (step 5).
  - { name: SHIP_VIA,            value: "firehose" }          # illustrative (step 3 / step 5)
  - { name: FIREHOSE_STREAM,     value: "lynceus-ingest" }    # illustrative`},
			{N: "2", Title: "PATH 2 — RESOURCE API ACCESS (IAM, RDS-ONLY)",
				Body: "A read-only role for control-plane metadata, limited strictly to RDS: rds:* actions are scoped to RDS ARNs and further gated on the lynceus=true resource tag; the CloudWatch read is restricted to the AWS/RDS namespace. Attach it to the collector service account via IRSA.",
				Code: `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "RdsMetadataOnly",
      "Effect": "Allow",
      "Action": [
        "rds:DescribeDBInstances",
        "rds:DescribeDBClusters",
        "rds:DescribeDBParameters",
        "rds:ListTagsForResource"
      ],
      "Resource": [
        "arn:aws:rds:*:<acct>:db:*",
        "arn:aws:rds:*:<acct>:cluster:*",
        "arn:aws:rds:*:<acct>:pg:*"
      ],
      "Condition": { "StringEquals": { "aws:ResourceTag/lynceus": "true" } }
    },
    {
      "Sid": "CloudWatchRdsNamespaceOnly",
      "Effect": "Allow",
      "Action": ["cloudwatch:GetMetricData", "cloudwatch:ListMetrics"],
      "Resource": "*",
      "Condition": { "StringEquals": { "cloudwatch:namespace": "AWS/RDS" } }
    }
  ]
}`},
			{N: "3", Title: "PATH 3 — FIREHOSE INGESTION (CONTROLLED INGRESS)",
				Body: "All AWS-side data enters Lynceus through a Firehose delivery stream that you own — the agent writes to it, CloudWatch Metric Streams and log subscription filters feed it, and a single HTTP delivery leaves your account. That one stream is your ingress control point: buffer sizing, an optional Lambda transform to drop or redact records before they leave, include-filters per namespace, and IAM control over exactly which producers may write.",
				Code: `producers:   agent (k8s) ............ firehose:PutRecordBatch
             CW Metric Stream ....... include_filter: AWS/RDS
             CW Logs sub filter ..... /aws/rds/instance/*/postgresql
                       |
                       v
 stream:     Firehose "lynceus-ingest"        <- YOUR control point
 controls:   buffering 1-5 MB / 60 s
             Lambda transform (drop / redact before egress)
             IAM: only the collector role may PutRecord
                       |
                       v
 delivery:   https://ingest.<region>.lynceus.io/v1/ingest
 auth:       access_key = <ingest token>      # SD MENU / COLLECTORS
 tenant:     X-Lynceus-Tenant: <org-id>       # common attribute`},
			{N: "4", Title: "QUERYSET MAPPING",
				Body: "Whether pulled (path 2) or streamed (path 3), metrics are mapped onto fixed Lynceus series ids (ship_to) so they land in the right node and database views.",
				Code: `querysets:
  - id: rds-core
    provider: aws
    namespace: AWS/RDS
    discover: { by: tag, key: lynceus, value: "true" }
    period: 60s
    metrics:
      - { name: CPUUtilization,      ship_to: node.cpu }
      - { name: FreeableMemory,      ship_to: node.mem_free }
      - { name: FreeStorageSpace,    ship_to: node.disk_free }
      - { name: ReadIOPS,            ship_to: node.io.read }
      - { name: WriteIOPS,           ship_to: node.io.write }
      - { name: DatabaseConnections, ship_to: pg.connections }
      - { name: ReplicaLag,          ship_to: pg.replica_lag }`},
			{N: "5", Title: "TERRAFORM",
				Body: "The whole AWS side as Terraform — scoped IAM policy, metric stream limited to AWS/RDS, and the Firehose delivery with endpoint, auth key and tenant header.",
				Code: `resource "aws_iam_policy" "lynceus_rds_read" {
  name   = "LynceusRdsRead"
  policy = file("lynceus-rds-policy.json")   # step 2
}

resource "aws_iam_role" "lynceus_collector" {
  name               = "LynceusCollector"
  assume_role_policy = data.aws_iam_policy_document.eks_oidc.json
}

resource "aws_iam_role_policy_attachment" "lynceus" {
  role       = aws_iam_role.lynceus_collector.name
  policy_arn = aws_iam_policy.lynceus_rds_read.arn
}

resource "aws_cloudwatch_metric_stream" "lynceus" {
  name          = "lynceus-rds"
  role_arn      = aws_iam_role.metric_stream.arn
  firehose_arn  = aws_kinesis_firehose_delivery_stream.lynceus.arn
  output_format = "opentelemetry1.0"

  include_filter { namespace = "AWS/RDS" }   # RDS only
}

resource "aws_kinesis_firehose_delivery_stream" "lynceus" {
  name        = "lynceus-ingest"
  destination = "http_endpoint"

  http_endpoint_configuration {
    name               = "lynceus"
    url                = "https://ingest.${var.region}.lynceus.io/v1/ingest"
    access_key         = var.lynceus_ingest_token   # SD MENU / COLLECTORS
    buffering_size     = 4    # MB — ingress control
    buffering_interval = 60   # seconds

    request_configuration {
      common_attributes {
        name  = "X-Lynceus-Tenant"
        value = var.lynceus_tenant_id
      }
    }
  }
}

# only the collector role may write into the stream
resource "aws_iam_role_policy" "collector_put" {
  role = aws_iam_role.lynceus_collector.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["firehose:PutRecord", "firehose:PutRecordBatch"]
      Resource = aws_kinesis_firehose_delivery_stream.lynceus.arn
    }]
  })
}`},
			{N: "6", Title: "VERIFY",
				Body: `kubectl logs deploy/lynceus-collector for paths 1–2 ("queryset rds-core: N series shipped"); for path 3, the Firehose destination error rate should be 0 and the instance appears under Database ▸ Nodes within ~60s with source "CloudWatch". Multi-AZ standbys remain a blind spot until promoted.`,
				Code: ""},
		},
	},
	ProviderSetupAzure: {
		Intro: "Azure Flexible Server exposes metrics through Azure Monitor. The collector authenticates with a managed identity (or app registration) and normalizes metrics through the same queryset mechanism.",
		Steps: []GuideStep{
			{N: "1", Title: "GRANT MONITORING READER",
				Body: "Assign the collector identity Monitoring Reader on the resource group holding your Flexible Servers.",
				Code: `az role assignment create \
  --assignee <collector-client-id> \
  --role "Monitoring Reader" \
  --scope /subscriptions/<sub>/resourceGroups/<rg>`},
			{N: "2", Title: "COLLECTOR CREDENTIALS",
				Body: "Provide tenant, subscription and client ids as env vars, or use workload identity on AKS.",
				Code: `env:
  - { name: AZURE_TENANT_ID,       value: "<tenant>" }
  - { name: AZURE_SUBSCRIPTION_ID, value: "<sub>" }
  - { name: AZURE_CLIENT_ID,       value: "<client>" }`},
			{N: "3", Title: "CONFIGURE THE QUERYSET",
				Body: "Azure metric names differ from CloudWatch — the queryset normalizes them onto the same Lynceus series ids.",
				Code: `querysets:
  - id: azure-flex-core
    provider: azure
    resource_type: Microsoft.DBforPostgreSQL/flexibleServers
    period: 60s
    metrics:
      - { name: cpu_percent,        ship_to: node.cpu }
      - { name: memory_percent,     ship_to: node.mem }
      - { name: storage_percent,    ship_to: node.disk }
      - { name: iops,               ship_to: node.io.total }
      - { name: active_connections, ship_to: pg.connections }
      - { name: physical_replication_delay_in_seconds, ship_to: pg.replica_lag }`},
			{N: "4", Title: "VERIFY",
				Body: "A zone-redundant HA standby has no endpoint; its metrics arrive through the server resource, and Lynceus renders it as a standby with provider-only visibility.",
				Code: ""},
		},
	},
	ProviderSetupPlanetScale: {
		Intro: "PlanetScale ships data differently: an org-level Prometheus endpoint with API-driven service discovery. The collector scrapes it directly — no cloud IAM involved and no standby blind spot.",
		Steps: []GuideStep{
			{N: "1", Title: "CREATE A SERVICE TOKEN",
				Body: "In PlanetScale organization settings, create a service token and grant it read_metrics_endpoints. Store the id and token as the secret lynceus-pscale.",
				Code: ""},
			{N: "2", Title: "POINT THE COLLECTOR AT THE ORG",
				Body: "HTTP service discovery finds every Postgres branch in the org and refreshes the list every 10 minutes.",
				Code: `scrape:
  - job: planetscale-postgres
    http_sd:
      url: https://api.planetscale.com/v1/organizations/<org>/metrics
      auth: "token <TOKEN_ID>:<TOKEN>"
      refresh: 10m
    interval: 30s`},
			{N: "3", Title: "CONFIGURE THE QUERYSET",
				Body: "Prometheus series are relabeled into Lynceus series; primaries and replicas report individually.",
				Code: `querysets:
  - id: pscale-core
    provider: planetscale
    metrics:
      - { match: pscale_cpu_utilization,           ship_to: node.cpu }
      - { match: pscale_memory_utilization,        ship_to: node.mem }
      - { match: pscale_connections,               ship_to: pg.connections }
      - { match: pscale_replication_lag_seconds,   ship_to: pg.replica_lag }`},
			{N: "4", Title: "VERIFY",
				Body: "Each branch appears as its own cluster with per-instance nodes. There is no host shell — node metrics come exclusively from the metrics endpoint.",
				Code: ""},
		},
	},
}
