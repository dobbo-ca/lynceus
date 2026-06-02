package logparse

// EventType is the canonical, T1-safe classification of a log line.
// New event types are added here AND a matching rule is added in
// rules.go. Strings are intentionally dotted hierarchies so consumers
// can filter on prefixes (e.g. all "vacuum.*").
type EventType string

const (
	EventUnclassified EventType = "log.unclassified"

	EventConnectionAuthorized   EventType = "connection.authorized"
	EventConnectionReceived     EventType = "connection.received"
	EventConnectionDisconnected EventType = "connection.disconnected"
	EventConnectionAuthFailed   EventType = "connection.auth_failed"

	EventCheckpointStarting EventType = "checkpoint.starting"
	EventCheckpointComplete EventType = "checkpoint.completed"

	EventVacuumCompleted     EventType = "vacuum.completed"
	EventAutovacuumCompleted EventType = "vacuum.autovacuum_completed"

	EventLockDeadlock      EventType = "lock.deadlock_detected"
	EventLockAcquiredAfter EventType = "lock.acquired_after_wait"

	EventQueryDuration        EventType = "query.duration"
	EventQueryCanceledTimeout EventType = "query.canceled_due_to_timeout"

	EventErrorConstraintViolation EventType = "error.constraint_violation"
	EventErrorSyntax              EventType = "error.syntax"

	EventAutoExplainPlan EventType = "auto_explain.plan"
	EventTempFileCreated EventType = "temp_file.created"
)
