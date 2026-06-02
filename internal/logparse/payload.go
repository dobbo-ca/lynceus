package logparse

// PayloadTier classifies a LogPayload at runtime so downstream filters
// and the wire layer can refuse to ship sensitive payload unless the
// destination server has T2 enabled.
type PayloadTier int

const (
	TierEmpty     PayloadTier = 0
	TierSensitive PayloadTier = 2
)

// LogPayload is the sensitive (T2) component of a parsed log line.
// Every field below MAY contain literal values from the monitored
// database. Callers MUST run PII filters (filter_log_secret etc.) over
// these fields before transmission, and MUST NOT attach a non-empty
// LogPayload to a T1 wire message.
type LogPayload struct {
	Message       string
	Detail        string
	Hint          string
	StatementText string
	InternalQuery string
	ContextLine   string
}

// Tier reports TierEmpty if every field is empty, otherwise
// TierSensitive. There is no in-between: any non-empty payload requires
// T2 treatment.
func (p LogPayload) Tier() PayloadTier {
	if p.Message == "" && p.Detail == "" && p.Hint == "" &&
		p.StatementText == "" && p.InternalQuery == "" && p.ContextLine == "" {
		return TierEmpty
	}
	return TierSensitive
}
