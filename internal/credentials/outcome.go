package credentials

// Outcome is the small, honest UNLOCK/DECRYPT-time vocabulary: host-side
// facts established at the prompt and under the setup lock. The zero value
// "" means the step succeeded. Credentials are BLOCKING, so a failure
// outcome names the reason a launch STOPPED; only the deliberate skip is
// ever recorded with a launch. Deliberately NO quarantine, foreign-vault,
// recipient-mismatch, snapshot-mismatch, or restart-discriminator states —
// those named adversary conditions that are out of the feature's threat
// model. Delivery's own words ("delivered", "not-delivered", the late
// hedge) are NOT constants here: they are the inject's stderr reporting,
// spoken as delivery happens and never recorded (the launch record is
// written pre-start; there is no live-state surface).
type Outcome string

const (
	// OutcomeSkippedDeclined: --credentials=skip, the one deliberate way to
	// launch without the declared rows.
	OutcomeSkippedDeclined Outcome = "skipped-declined"
	// OutcomeMissingValue: the vault holds no entry under this name.
	OutcomeMissingValue Outcome = "missing-value"
	// OutcomeRowUndecryptable: corrupt or oversize ciphertext, or one
	// encrypted to a different recipient.
	OutcomeRowUndecryptable Outcome = "row-undecryptable"
	// OutcomeRowMismatch: the payload's key or kind disagrees with the row
	// carrying it — the accident guard (a blob swapped between rows,
	// replayed onto a renamed key, transplanted from another file), not an
	// integrity mechanism.
	OutcomeRowMismatch Outcome = "row-mismatch"
	// OutcomeUnsupportedFormat: a payload this byre does not understand
	// (a future format version), or a value that does not fit its declared
	// kind (an env value carrying NUL or over the env cap).
	OutcomeUnsupportedFormat Outcome = "unsupported-format"
)
