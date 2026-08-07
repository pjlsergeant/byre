package credentials

// Outcome is the small, honest UNLOCK/DECRYPT-time vocabulary (ADR 0057):
// host-side facts established at the prompt and under the setup lock,
// reported there and recorded with the launch. The zero value "" means the
// step succeeded. Deliberately NO quarantine, foreign-vault,
// recipient-mismatch, snapshot-mismatch, or restart-discriminator states —
// those named adversary conditions that are out of the feature's threat
// model. Delivery's own words ("delivered", "not-delivered", the late
// hedge) are NOT constants here: they are the inject's stderr reporting,
// spoken as delivery happens and never recorded (the launch record is
// written pre-start; there is no live-state surface).
type Outcome string

const (
	// OutcomeSkippedDeclined: the user pressed Enter at the unlock prompt.
	OutcomeSkippedDeclined Outcome = "skipped-declined"
	// OutcomeSkippedNonTTY: no terminal to prompt on; launch proceeds
	// without credentials, machine-readably noticed.
	OutcomeSkippedNonTTY Outcome = "skipped-nontty"
	// OutcomeUnlockFailed: passphrase attempts exhausted, or a corrupt,
	// oversize, or absent identity.
	OutcomeUnlockFailed Outcome = "unlock-failed"
	// OutcomeMissingValue: declared, but the vault holds no entry.
	OutcomeMissingValue Outcome = "missing-value"
	// OutcomeEntryUndecryptable: corrupt or oversize ciphertext, or one
	// encrypted to a different recipient.
	OutcomeEntryUndecryptable Outcome = "entry-undecryptable"
	// OutcomeEntryMismatch: the payload's name or project-id disagrees with
	// where the file sits — the accident guard (cross-project copy,
	// wrong-project restore), not an integrity mechanism.
	OutcomeEntryMismatch Outcome = "entry-mismatch"
	// OutcomeUnsupportedFormat: a payload this byre does not understand
	// (a future format version), or a value that does not fit its declared
	// kind (an env value carrying NUL or over the env cap).
	OutcomeUnsupportedFormat Outcome = "unsupported-format"
)
