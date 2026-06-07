package collector

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/dobbo-ca/lynceus/internal/logparse"
	"github.com/dobbo-ca/lynceus/internal/normalize"
	"github.com/dobbo-ca/lynceus/internal/planextract"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// autoExplainPrefix matches the "duration: N ms plan:" header auto_explain
// writes before the plan body. Mirrors the classifier rule (rules.go) so the
// remainder is the plan body (JSON when log_format=json).
var autoExplainPrefix = regexp.MustCompile(`(?s)^duration: [0-9.]+ ms\s+plan:\s*`)

// ExtractPlans turns auto_explain.plan log records into normalized T1
// QueryPlans. events[i]/payloads[i] are the parallel slices returned by
// logparse.ParseStream. Only EventAutoExplainPlan records are processed; the
// plan body lives in the collector-local (T2) payload Message. Non-JSON plan
// bodies are dropped, never guessed. The returned plans carry no literal —
// see the QueryPlan privacy contract test.
func ExtractPlans(events []logparse.LogEvent, payloads []logparse.LogPayload) []*lynceusv1.QueryPlan {
	var out []*lynceusv1.QueryPlan
	for i := range events {
		if events[i].EventType != logparse.EventAutoExplainPlan {
			continue
		}
		body := strings.TrimSpace(autoExplainPrefix.ReplaceAllString(payloads[i].Message, ""))
		if body == "" {
			continue
		}
		fp := planFingerprint(payloads[i], body)
		qp, err := planextract.Extract([]byte(body), fp, events[i].OccurredAt)
		if err != nil {
			continue // unsupported (non-JSON) format — drop rather than guess
		}
		out = append(out, qp)
	}
	return out
}

// planFingerprint computes a literal-free fingerprint for the statement the
// plan belongs to, collector-locally. It prefers the record's StatementText
// and falls back to the plan body's "Query Text". Neither string leaves the
// collector — only the resulting fingerprint hash travels on the wire.
func planFingerprint(p logparse.LogPayload, body string) string {
	stmt := p.StatementText
	if stmt == "" {
		stmt = queryTextFromBody(body)
	}
	if stmt == "" {
		return ""
	}
	fp, err := normalize.Fingerprint(stmt)
	if err != nil {
		return ""
	}
	return fp
}

// queryTextFromBody pulls the "Query Text" out of an auto_explain JSON body.
// Used only collector-locally for fingerprinting; the text itself is never
// shipped (planextract.Extract deliberately ignores it).
func queryTextFromBody(body string) string {
	var envs []struct {
		QueryText string `json:"Query Text"`
	}
	if err := json.Unmarshal([]byte(body), &envs); err != nil || len(envs) == 0 {
		return ""
	}
	return envs[0].QueryText
}
