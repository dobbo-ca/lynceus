package collector

import (
	"github.com/dobbo-ca/lynceus/internal/insight"
	"github.com/dobbo-ca/lynceus/internal/logparse"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// LogPipeline drains a LogSource one bounded chunk at a time, parses it with
// logparse.ParseStream, and produces T1 outputs only: classified LogEvents and
// auto_explain plans extracted to QueryPlans. The T2 payloads returned by
// ParseStream reach ExtractPlans (which strips literals) and nothing else —
// they never leave the collector.
type LogPipeline struct {
	src           LogSource
	opts          logparse.Options
	serverID      string
	detectLocally bool
}

// NewLogPipeline returns a LogPipeline reading from src. opts.Format selects the
// stderr/csv parser. When detectLocally is true, Drain runs insight.DetectPlans
// over the extracted plans; those insights are returned for the caller to log
// (a count), never shipped on the wire.
func NewLogPipeline(src LogSource, serverID string, opts logparse.Options, detectLocally bool) *LogPipeline {
	return &LogPipeline{src: src, opts: opts, serverID: serverID, detectLocally: detectLocally}
}

// DrainResult is one poll's worth of T1 outputs. LogEvents and QueryPlans are
// shipped; Insights are populated only when detectLocally and are NOT shipped.
type DrainResult struct {
	LogEvents  []*lynceusv1.LogEvent
	QueryPlans []*lynceusv1.QueryPlan
	Insights   []insight.Insight
}

// Drain reads one bounded chunk from the source and returns its T1 outputs.
// An empty chunk yields a zero DrainResult and a nil error. A parse error is
// returned to the caller; any events/payloads parsed before the error are not
// discarded by ParseStream, but Drain treats a non-nil error as authoritative.
func (p *LogPipeline) Drain() (DrainResult, error) {
	r, err := p.src.Read()
	if err != nil {
		return DrainResult{}, err
	}
	events, payloads, err := logparse.ParseStream(r, p.opts)
	if err != nil {
		return DrainResult{}, err
	}

	res := DrainResult{}
	for i := range events {
		res.LogEvents = append(res.LogEvents, toProtoLogEvent(&events[i]))
	}
	res.QueryPlans = ExtractPlans(events, payloads)
	if p.detectLocally {
		res.Insights = insight.DetectPlans(res.QueryPlans)
	}
	return res, nil
}

// toProtoLogEvent maps a T1 logparse.LogEvent onto the wire LogEvent. It copies
// ONLY the classification fields of logparse.LogEvent (event.go:13) — it never
// reaches into the parallel LogPayload, so no literal can travel.
func toProtoLogEvent(e *logparse.LogEvent) *lynceusv1.LogEvent {
	return &lynceusv1.LogEvent{
		EventType:       string(e.EventType),
		Severity:        e.Severity.String(),
		OccurredAtUnix:  e.OccurredAt.Unix(),
		LoggedAtUnix:    e.LoggedAt.Unix(),
		Pid:             e.PID,
		BackendType:     e.BackendType,
		DatabaseName:    e.DatabaseName,
		UserName:        e.UserName,
		ApplicationName: e.AppName,
		ClientAddrHash:  e.ClientAddrHash,
		SqlState:        e.SQLState,
		SessionLineNum:  e.SessionLineNum,
		TransactionId:   e.TransactionID,
	}
}
