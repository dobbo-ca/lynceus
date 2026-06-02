package logparse

import "regexp"

// DefaultRules is the v1 vocabulary. Each rule's regex is anchored
// loosely (we use .MatchString, not full-match) so trailing fields
// don't break it. Ordering matters: more specific rules first.
//
// To add a new event:
//  1. Add an EventType constant in event_type.go.
//  2. Append a Rule here, BEFORE any broader rule it could collide with.
//  3. Add a test case in classifier_test.go.
func DefaultRules() []Rule {
	var (
		// auto_explain emits "duration: NN ms  plan:" — must match before
		// the bare "duration: NN ms  statement:" rule.
		reAutoExplain     = regexp.MustCompile(`(?m)^duration: [0-9.]+ ms\s+plan:`)
		reDuration        = regexp.MustCompile(`(?m)^duration: [0-9.]+ ms\s+(statement|execute|parse|bind)\b`)
		reConnAuthorized  = regexp.MustCompile(`^connection authorized:`)
		reConnReceived    = regexp.MustCompile(`^connection received:`)
		reDisconnect      = regexp.MustCompile(`^disconnection: session time:`)
		reAuthFailed      = regexp.MustCompile(`^(password authentication failed|no pg_hba\.conf entry|authentication failed)`)
		reCheckpointStart = regexp.MustCompile(`^checkpoint starting:`)
		reCheckpointDone  = regexp.MustCompile(`^checkpoint complete:`)
		reAutovacuum      = regexp.MustCompile(`^automatic vacuum of table`)
		reManualVacuum    = regexp.MustCompile(`^vacuuming "[^"]+"`)
		reDeadlock        = regexp.MustCompile(`^deadlock detected`)
		reLockAcquired    = regexp.MustCompile(`acquired \w+Lock on .* after [0-9.]+ ms`)
		reTimeoutCancel   = regexp.MustCompile(`^canceling statement due to (statement|lock|idle-in-transaction-session) timeout`)
		reConstraint      = regexp.MustCompile(`violates (unique|foreign key|check|not-null) constraint`)
		reSyntax          = regexp.MustCompile(`^syntax error at or near`)
		reTempFile        = regexp.MustCompile(`^temporary file:`)
	)

	isError := func(rec RawRecord) bool {
		return rec.Severity == SeverityError || rec.Severity == SeverityFatal
	}

	return []Rule{
		{EventType: EventAutoExplainPlan, Match: reMatch(reAutoExplain)},
		{EventType: EventQueryDuration, Match: reMatch(reDuration)},

		{EventType: EventConnectionAuthorized, Match: reMatch(reConnAuthorized)},
		{EventType: EventConnectionReceived, Match: reMatch(reConnReceived)},
		{EventType: EventConnectionDisconnected, Match: reMatch(reDisconnect)},
		{EventType: EventConnectionAuthFailed, Match: reMatchWith(reAuthFailed, func(r RawRecord) bool {
			return r.Severity == SeverityFatal || r.Severity == SeverityError
		})},

		{EventType: EventCheckpointStarting, Match: reMatch(reCheckpointStart)},
		{EventType: EventCheckpointComplete, Match: reMatch(reCheckpointDone)},

		{EventType: EventAutovacuumCompleted, Match: reMatch(reAutovacuum)},
		{EventType: EventVacuumCompleted, Match: reMatch(reManualVacuum)},

		{EventType: EventLockDeadlock, Match: reMatchWith(reDeadlock, isError)},
		{EventType: EventLockAcquiredAfter, Match: reMatch(reLockAcquired)},

		{EventType: EventQueryCanceledTimeout, Match: reMatchWith(reTimeoutCancel, isError)},

		{EventType: EventErrorConstraintViolation, Match: reMatchWith(reConstraint, isError)},
		{EventType: EventErrorSyntax, Match: reMatchWith(reSyntax, isError)},

		{EventType: EventTempFileCreated, Match: reMatch(reTempFile)},
	}
}
