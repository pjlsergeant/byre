package credentials

// Outcome is the small, honest per-launch vocabulary the design brief pins
// (wip/secure-credentials.md "Outcome vocabulary"): host-side facts reported
// at the unlock prompt and recorded with the launch. Deliberately NO
// quarantine, foreign-vault, recipient-mismatch, snapshot-mismatch, or
// restart-discriminator states — those named adversary conditions that are
// out of the feature's threat model.
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
	// OutcomeDelivered: the value landed on the session tmpfs.
	OutcomeDelivered Outcome = "delivered"
	// OutcomeNotDelivered: delivery timed out or the inject failed; the box
	// launched without credentials.
	OutcomeNotDelivered Outcome = "not-delivered"
)
