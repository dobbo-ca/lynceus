package logparse

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// Rule is one entry in the classification table.
//
// New event types are added by appending a Rule to DefaultRules. The
// classifier is a linear scan in declaration order, so put more
// specific rules above more general ones (e.g. auto_explain.plan
// must come BEFORE query.duration — both start with "duration: ").
type Rule struct {
	EventType EventType
	// Match returns true if the rule applies. RawRecord is supplied
	// rather than just the message so rules can also inspect severity,
	// SQLSTATE, etc.
	Match func(RawRecord) bool
}

// Classifier turns a RawRecord into a (LogEvent, LogPayload) pair.
type Classifier struct {
	rules []Rule
}

// NewClassifier returns a Classifier that scans rules in order.
func NewClassifier(rules []Rule) *Classifier {
	return &Classifier{rules: rules}
}

// Classify returns the T1 event and the T2 payload split out from rec.
// The returned LogEvent carries no payload-bearing field; the
// LogPayload carries every sensitive substring.
func (c *Classifier) Classify(rec *RawRecord) (LogEvent, LogPayload) {
	ev := LogEvent{
		EventType:      EventUnclassified,
		Severity:       rec.Severity,
		OccurredAt:     rec.OccurredAt,
		LoggedAt:       rec.LoggedAt,
		PID:            rec.PID,
		BackendType:    rec.BackendType,
		DatabaseName:   rec.DatabaseName,
		UserName:       rec.UserName,
		AppName:        rec.AppName,
		ClientAddrHash: hashClientAddr(rec.ClientAddr),
		SQLState:       rec.SQLState,
		SessionLineNum: rec.SessionLine,
		TransactionID:  rec.TxnID,
	}
	for _, r := range c.rules {
		if r.Match(*rec) {
			ev.EventType = r.EventType
			break
		}
	}
	payload := LogPayload{
		Message:       rec.Message,
		Detail:        rec.Detail,
		Hint:          rec.Hint,
		StatementText: rec.StatementText,
		InternalQuery: rec.InternalQuery,
		ContextLine:   rec.ContextLine,
	}
	return ev, payload
}

func hashClientAddr(addr string) string {
	if addr == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(addr))
	return hex.EncodeToString(sum[:])
}

func reMatch(re *regexp.Regexp) func(RawRecord) bool {
	return func(rec RawRecord) bool { return re.MatchString(rec.Message) }
}

func reMatchWith(re *regexp.Regexp, pre func(RawRecord) bool) func(RawRecord) bool {
	return func(rec RawRecord) bool {
		return pre(rec) && re.MatchString(rec.Message)
	}
}
