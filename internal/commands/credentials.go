package commands

// The launch side of project credentials: resolve the cascade's encrypted
// env_from_host rows, unlock each contributing file's identity before the
// setup lock, decrypt the winning rows under it, and inject the values onto
// the session tmpfs after start.
//
// Credentials are BLOCKING. Every failure — an unparseable file, a row whose
// file carries no [credentials] block, a wrong passphrase after its attempts,
// a blob that does not open or is stamped for another row — stops the launch
// naming the file, the key, and the remedy. There is no host-side fail-open
// path: a box that silently launches without a credential the config declares
// is a box whose agent fails in a way nobody can read.

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	xterm "github.com/charmbracelet/x/term"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/runner"
)

const (
	// credTmpfsTarget is the session tmpfs the receiver writes and the
	// launcher reads — an ADR 0052 managed path.
	credTmpfsTarget = "/run/byre"
	// credPassphraseAttempts bounds the wrong-passphrase re-prompt per file:
	// enough for typos, bounded against an endless prompt loop.
	credPassphraseAttempts = 3
	// credInjectRunningWait bounds the poll for the container to be RUNNING.
	// It does not race the launcher: the launcher's own wait clock only
	// starts once the box starts, which is exactly when this poll ends.
	credInjectRunningWait = 60 * time.Second
	// credInjectDeadline bounds the exec itself, and THIS one is pinned
	// under the launcher's wait (BYRE_CRED_WAIT, default 20s in
	// launcher.sh): both clocks start at box start, so an exec that
	// completes inside this deadline lands before the launcher's wait
	// expires — "delivered" then honestly means the exports happen. The
	// margin absorbs the launcher's pre-wait lines; a stream is a few MiB
	// at most, so a full deadline spent is a wedged daemon. A test pins
	// deadline + margin <= the launcher default so the two cannot drift
	// apart silently (the handoff's wait >= deadline + skew pin).
	credInjectDeadline = 15 * time.Second
	// credLateThreshold decides which delivery line is honest. The inject
	// goroutine's start precedes StartAttach, so elapsed-since-entry always
	// OVERESTIMATES elapsed-since-box-start — an inject that completes
	// inside this threshold is therefore PROVABLY inside the launcher's
	// wait (default 20s), and only then does byre say a plain "delivered".
	// Anything slower gets the hedge: the values are on the tmpfs, but the
	// box's own wait may already have expired and failed it closed.
	// Measurement, not clock-epoch assumptions — a slow engine cannot make
	// byre lie.
	credLateThreshold = 18 * time.Second
)

// CredentialMode is the --credentials answer: how (or whether) this launch
// gets its passphrases. Blocking is the rule in every mode — skip is the one
// deliberate way to launch without the values, and it says so in the record.
type CredentialMode string

const (
	// CredentialAsk prompts on the terminal, per contributing file.
	CredentialAsk CredentialMode = "ask"
	// CredentialSkip launches deliberately without credentials.
	CredentialSkip CredentialMode = "skip"
	// CredentialStdin reads passphrase lines from stdin, each tried against
	// every still-locked identity in file order.
	CredentialStdin CredentialMode = "stdin"
)

// ParseCredentialMode maps the --credentials flag value; "" is the default.
func ParseCredentialMode(v string) (CredentialMode, error) {
	switch CredentialMode(v) {
	case "", CredentialAsk:
		return CredentialAsk, nil
	case CredentialSkip:
		return CredentialSkip, nil
	case CredentialStdin:
		return CredentialStdin, nil
	}
	return "", fmt.Errorf("--credentials %q: want ask|skip|stdin", echoValue(v))
}

// echoValue bounds an echoed flag value so a pasted wall of input cannot
// become the message.
func echoValue(v string) string {
	r := []rune(v)
	if len(r) > 32 {
		return string(append(r[:32], '…'))
	}
	return v
}

// readPassphrase is the masked passphrase read, a seam so tests can answer
// without a terminal. The real read is byte-masked on stdin (the prompt is
// TTY-gated by the caller); the trailing newline echo keeps the next line
// clean.
var readPassphrase = func(w io.Writer, prompt string) (string, error) {
	fmt.Fprint(w, prompt)
	defer fmt.Fprintln(w)
	b, err := xterm.ReadPassword(os.Stdin.Fd())
	return string(b), err
}

// unlockedFiles maps a cascade file's LABEL to the identity that opens its
// rows. The label is unique within one cascade, and it is also what every
// message names, so nothing here has to carry a second key.
type unlockedFiles map[string]*credentials.Identity

// credentialDisplay renders a cascade label the way the plan line and the
// prompts speak it: "layer:acme" is a config-internal spelling, "layer acme"
// is a sentence.
func credentialDisplay(label string) string {
	return strings.Replace(label, ":", " ", 1)
}

// credentialPlanLine names every contributing file and how many rows it
// carries, printed BEFORE the first prompt so a user knows how many
// passphrases they are about to be asked for and why.
func credentialPlanLine(groups []config.CredentialFile) string {
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, fmt.Sprintf("%s (%d)", credentialDisplay(g.Label), len(g.Rows)))
	}
	return "byre: unlocking credentials: " + strings.Join(parts, ", ")
}

// unlockCredentials is the pre-lock unlock: for each contributing file,
// root-most first, obtain the identity that opens its rows. The expensive
// scrypt unwrap happens HERE, before any lock — holding the setup lock across
// it would stall sibling worktrees for an authentication cost.
//
// Each entered passphrase is tried on every STILL-LOCKED identity before the
// next prompt: people reuse passphrases across their own files, and asking
// three times for the same one is the kind of friction that makes a person
// turn the feature off.
//
// Returns an error on anything that cannot be unlocked. Blocking is the whole
// contract, so this is the first of the two places a launch stops.
func unlockCredentials(s Streams, mode CredentialMode, groups []config.CredentialFile) (unlockedFiles, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	if mode == CredentialSkip {
		fmt.Fprintf(s.Err, "byre: credentials: %s — launching without the %d declared row(s).\n",
			credentials.OutcomeSkippedDeclined, countRows(groups))
		return nil, nil
	}
	// A row whose file carries no identity can never be opened, and no
	// neighbour's block may stand in for it. Say so before spending a single
	// passphrase prompt.
	for _, g := range groups {
		if !g.HasBlock {
			return nil, fmt.Errorf("credentials: %s carries %d encrypted row(s) (%s) but no [credentials] block — the rows were copied without the identity that opens them. Set one of them here to mint an identity (byre credentials set %s), or remove the rows",
				credentialAttribution(g), len(g.Rows), rowKeys(g), g.Rows[0].Key)
		}
	}
	fmt.Fprintln(s.Err, credentialPlanLine(groups))
	open := unlockedFiles{}
	if mode == CredentialStdin {
		return open, unlockFromStdin(s, groups, open)
	}
	if !s.TTY {
		return nil, fmt.Errorf("credentials: %d row(s) need a passphrase and there is no terminal to prompt on. Pipe the passphrases in with --credentials=stdin, or launch deliberately without them with --credentials=skip", countRows(groups))
	}
	for _, g := range groups {
		if open[g.Label] != nil {
			continue // a passphrase entered for an earlier file already opened it
		}
		if err := unlockOneByPrompt(s, g, groups, open); err != nil {
			return nil, err
		}
	}
	return open, nil
}

// unlockOneByPrompt asks for g's passphrase, bounded, trying each answer
// against every still-locked file so a reused passphrase is entered once.
func unlockOneByPrompt(s Streams, g config.CredentialFile, groups []config.CredentialFile, open unlockedFiles) error {
	for attempt := 1; attempt <= credPassphraseAttempts; attempt++ {
		pw, err := readPassphrase(s.Err, fmt.Sprintf("byre: passphrase for %s credentials: ", credentialDisplay(g.Label)))
		if err != nil {
			return fmt.Errorf("credentials: could not read the passphrase for %s: %w", credentialAttribution(g), err)
		}
		if pw == "" {
			fmt.Fprintf(s.Err, "byre: credentials: %s.\n", credentials.EmptyPassphraseWorthless)
			continue
		}
		if err := tryPassphrase(pw, groups, open); err != nil {
			return err
		}
		if open[g.Label] != nil {
			return nil
		}
		if attempt < credPassphraseAttempts {
			fmt.Fprintln(s.Err, "byre: credentials: wrong passphrase — try again.")
		}
	}
	// NOT "rotate it with rekey": rekey opens with a prompt for the CURRENT
	// passphrase, which is the thing this user has just failed three times.
	// A lost passphrase makes these values unrecoverable, and the only real
	// remedy is to replace the identity — which means taking out the rows and
	// the block that no longer opens them, then setting them again (the same
	// "re-set the values under a new identity" the rekey notice speaks of).
	return fmt.Errorf("credentials: wrong passphrase for %s after %d attempts — nothing was launched. `byre credentials rekey` cannot help here: it asks for this same passphrase first. If it is lost these values cannot be recovered — take the rows out (byre credentials unset %s), delete that file's [credentials] block, and set them again under a new identity. To launch without these rows: --credentials=skip",
		credentialAttribution(g), credPassphraseAttempts, g.Rows[0].Key)
}

// unlockFromStdin is the non-interactive mode: passphrase lines, each tried
// against every still-locked identity in file order. EOF with anything still
// locked stops the launch — the values were declared and cannot be delivered.
func unlockFromStdin(s Streams, groups []config.CredentialFile, open unlockedFiles) error {
	sc := bufio.NewScanner(s.In)
	// A passphrase is a line; the cap keeps a pathological stream from
	// growing the buffer without bound.
	sc.Buffer(make([]byte, 0, 4<<10), 64<<10)
	for sc.Scan() {
		pw := sc.Text()
		if pw == "" {
			fmt.Fprintf(s.Err, "byre: credentials: %s — skipping an empty line.\n", credentials.EmptyPassphraseWorthless)
			continue
		}
		if err := tryPassphrase(pw, groups, open); err != nil {
			return err
		}
		if len(open) == len(groups) {
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("credentials: reading passphrases from stdin: %w", err)
	}
	var locked []string
	for _, g := range groups {
		if open[g.Label] == nil {
			locked = append(locked, credentialAttribution(g))
		}
	}
	return fmt.Errorf("credentials: stdin ended with %d file(s) still locked (%s) — nothing was launched. Supply their passphrases one per line, or launch without them with --credentials=skip",
		len(locked), strings.Join(locked, ", "))
}

// tryPassphrase attempts pw against every still-locked file. A wrong
// passphrase is silent (the caller re-asks); anything else — a corrupt or
// oversize identity blob — stops the launch, because re-asking cannot fix it.
func tryPassphrase(pw string, groups []config.CredentialFile, open unlockedFiles) error {
	for _, g := range groups {
		if open[g.Label] != nil {
			continue
		}
		id, err := credentials.UnwrapIdentity(g.Block.Identity, pw)
		if err == nil {
			open[g.Label] = id
			continue
		}
		if !errors.Is(err, credentials.ErrBadPassphrase) {
			return fmt.Errorf("credentials: %s: %w — nothing was launched", credentialAttribution(g), err)
		}
	}
	return nil
}

// credentialAttribution names a contributing file the way a refusal should:
// the label a user recognizes, and the path to edit when there is one.
func credentialAttribution(g config.CredentialFile) string {
	if g.Path == "" {
		return credentialDisplay(g.Label)
	}
	return credentialDisplay(g.Label) + " (" + g.Path + ")"
}

func rowKeys(g config.CredentialFile) string {
	keys := make([]string, 0, len(g.Rows))
	for _, r := range g.Rows {
		keys = append(keys, r.Key)
	}
	return strings.Join(keys, ", ")
}

func countRows(groups []config.CredentialFile) int {
	n := 0
	for _, g := range groups {
		n += len(g.Rows)
	}
	return n
}

// credPayload is the under-lock decrypt's product: what the inject streams,
// what the launch record notes, and what the chassis env/tmpfs need.
type credPayload struct {
	values   map[string][]byte // config key -> plaintext, held only until the inject
	manifest string            // the launcher's export map: "KEY kind" lines
	record   []launchCredential
	unlock   string // the record's unlock word
}

// The record's unlock vocabulary. "unlocked" is a record-only word; a
// deliberate skip records the outcome that names itself.
const launchCredentialUnlocked = "unlocked"

// launchCredentialScheduled is the record's word for a value that decrypted
// and was scheduled for delivery. Deliberately not "delivered": the record
// is written (content-addressed, immutable) before the container starts,
// and the design does not probe in-box state afterwards — the inject's own
// delivered/not-delivered outcome is reported on stderr as it happens.
const launchCredentialScheduled = "scheduled"

// decryptCredentialsLocked runs under the setup lock against the
// AUTHORITATIVE cascade read: with each identity already unwrapped, only the
// cheap per-row decrypts hold the lock. This is the second place a launch
// stops — a row that appeared while develop waited for the lock has no
// unlocked identity, and delivering some of a declared set is exactly the
// half-configured box the blocking rule exists to prevent.
func decryptCredentialsLocked(groups []config.CredentialFile, open unlockedFiles, skipped bool) (credPayload, error) {
	p := credPayload{values: map[string][]byte{}}
	if len(groups) == 0 {
		return p, nil
	}
	if skipped {
		p.unlock = string(credentials.OutcomeSkippedDeclined)
		return p, nil
	}
	p.unlock = launchCredentialUnlocked
	var manifest strings.Builder
	for _, g := range groups {
		id := open[g.Label]
		if id == nil {
			return credPayload{}, fmt.Errorf("credentials: %s appeared while develop waited for the setup lock, so its passphrase was never asked for — nothing was launched; re-run byre develop", credentialAttribution(g))
		}
		if !g.HasBlock {
			return credPayload{}, fmt.Errorf("credentials: %s lost its [credentials] block while develop waited for the setup lock — nothing was launched", credentialAttribution(g))
		}
		for _, row := range g.Rows {
			value, outcome, err := id.DecryptValue(row.Key, row.Kind, row.Blob)
			if err != nil {
				return credPayload{}, fmt.Errorf("credentials: %s: %s: %w — nothing was launched", credentialAttribution(g), outcome, err)
			}
			if verr := credentials.ValidateValue(value, row.Kind); verr != nil {
				return credPayload{}, fmt.Errorf("credentials: %s: %s: %v — nothing was launched", credentialAttribution(g), credentials.OutcomeUnsupportedFormat, verr)
			}
			if _, dup := p.values[row.Key]; dup {
				// Unreachable through EncryptedRows (one winner per key), and
				// stated anyway: two files delivering one key would export
				// whichever the map iteration happened to hand the receiver.
				return credPayload{}, fmt.Errorf("credentials: %s: %s is delivered twice — nothing was launched", credentialAttribution(g), row.Key)
			}
			p.values[row.Key] = value
			fmt.Fprintf(&manifest, "%s %s\n", row.Key, row.Kind)
			p.record = append(p.record, launchCredential{Key: row.Key, Kind: string(row.Kind), Source: g.Label, Outcome: launchCredentialScheduled})
		}
	}
	p.manifest = manifest.String()
	return p, nil
}

// refuseCredentialViewMismatch cross-checks develop's TWO independent reads of
// one cascade — the merged config (resolveHostEnv's hostEnvEncrypted rows) and
// the per-file credential view the unlock and decrypt ran on — at the last
// point before a container is created.
//
// They are read separately (config.Load, then config.CascadeFiles, whose
// contract lets an unreadable sublayer drop out), so a layer written between
// them can leave a key in the first and not the second. Nothing downstream
// would say so: an encrypted row never joins the `-e` export, so no prompt is
// missed loudly, no manifest entry appears, and if it was the only row the
// launcher is never even armed to wait — the agent would run without the value
// and byre would print nothing about it. That silent class is what this
// closes; the mismatch is transient by construction, so the remedy is to run
// again.
//
// Deliberately ONE-directional. Merged-has/file-lacks is the silent class
// above; the transient opposite — a row the file view carries that the merged
// config no longer declares — is accepted, because it delivers one extra value
// the user was prompted for and the launch record names, which is loud by
// construction rather than silent.
func refuseCredentialViewMismatch(hostEnv []hostEnvResult, values map[string][]byte) error {
	var missing []string
	for _, r := range hostEnv {
		if r.State != hostEnvEncrypted {
			continue
		}
		if _, ok := values[r.Key]; !ok {
			missing = append(missing, r.Key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("credentials: %s: declared as an encrypted row by the config cascade, and absent from the credential file view this launch unlocked and decrypted — the two reads of the cascade disagree, so a config layer changed between them; nothing was launched, re-run byre develop",
		strings.Join(missing, ", "))
}

// credStream frames the deliverable set for the receiver: a version line,
// the manifest first, values in key order, "done" last. Payloads ride
// single-line base64 so the receiver parses line-oriented and binary-safe.
func credStream(p credPayload) []byte {
	var b bytes.Buffer
	b.WriteString("byre-credentials 1\n")
	frame := func(name string, payload []byte) {
		b.WriteString("item " + name + "\n")
		b.WriteString(base64.StdEncoding.EncodeToString(payload))
		b.WriteByte('\n')
	}
	frame("manifest", []byte(p.manifest))
	keys := make([]string, 0, len(p.values))
	for k := range p.values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		frame(k, p.values[k])
	}
	b.WriteString("done\n")
	return b.Bytes()
}

// credExpectFlag is what BYRE_CRED_EXPECT carries. The launcher reads it as a
// flag, not a count ("wait bounded for .done, then export from the
// manifest"), and every renderer of the run argv — develop's create and the
// `byre dockerrun` ejection — must spell it the same way, or the ejected
// command is not the command develop runs.
const credExpectFlag = "1"

// credTmpfs sizes the session tmpfs for the deliverable set: the payload
// plus fixed headroom, so an fs-metadata surprise never truncates a value.
func credTmpfs(p credPayload, ident runner.Identity, eng runner.Engine) runner.TmpfsMount {
	var total int64
	for _, v := range p.values {
		total += int64(len(v))
	}
	total += int64(len(p.manifest))
	return credTmpfsFor(total, ident, eng)
}

// credTmpfsDeclared sizes the same mount from the DECLARED rows, for the
// ejection render — which holds no passphrase and so has no plaintext to
// measure. An age ciphertext is never shorter than its plaintext, so the blob
// lengths bound the delivery from above.
func credTmpfsDeclared(groups []config.CredentialFile, ident runner.Identity, eng runner.Engine) runner.TmpfsMount {
	var total int64
	for _, g := range groups {
		for _, r := range g.Rows {
			total += int64(len(r.Blob))
		}
	}
	return credTmpfsFor(total, ident, eng)
}

func credTmpfsFor(total int64, ident runner.Identity, eng runner.Engine) runner.TmpfsMount {
	return runner.TmpfsMount{
		Target: credTmpfsTarget,
		Size:   total + (1 << 20),
		Mode:   "0700",
		UID:    ident.UID,
		GID:    ident.GID,
		// Selects the podman rendering (--mount + U=true; docker's --tmpfs
		// uid= form is rejected there — see TmpfsMount).
		Podman: eng == runner.Podman,
	}
}

// credentialUnlockLine renders the record's unlock word for the status
// Credentials row: launch-time facts, never live-state claims (byre does
// not probe the box).
func credentialUnlockLine(unlock string) string {
	if unlock == launchCredentialUnlocked {
		return "unlocked (credentials were decrypted and handed to delivery)"
	}
	return unlock + " (launched without credentials)"
}

// runCredentialInject is delivery's host side, run concurrently with the
// attached session (the netns-hook pattern): wait for the box to be RUNNING
// (exec needs a live container), then pipe the framed stream to the baked
// receiver as the dev identity, bounded.
//
// A failure here is a LAUNCH failure, not a degraded launch: the box's own
// launcher fails closed on a delivery that never lands, so leaving the
// container running would leave a box that is about to exit anyway, and the
// error the user reads must be the real one. The returned error is the
// caller's to report; stopping the container is its job too (this goroutine
// does not own the session).
func runCredentialInject(r sessionRunner, warn io.Writer, label, containerID string, ident runner.Identity, stream []byte, epoch time.Time, done <-chan struct{}) error {
	// epoch was captured by develop SYNCHRONOUSLY before this goroutine was
	// spawned (and so before StartAttach could run): elapsed-since-epoch
	// bounds elapsed-since-box-start from above by construction, which is
	// what makes the delivered/late line honest (see credLateThreshold).
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	deadline := epoch.Add(credInjectRunningWait)
	for {
		if ids, err := r.RunningContainersByLabel(label); err == nil && len(ids) > 0 {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("credentials: the box never reached running within the wait, so nothing could be delivered")
		}
		select {
		case <-done:
			return nil // session over before the box ran; nothing to deliver to
		case <-tick.C:
		}
	}
	if _, err := r.ExecInputBounded(credInjectDeadline, containerID, ident.UID, ident.GID, bytes.NewReader(stream), gen.ReceiverPath); err != nil {
		return fmt.Errorf("credentials: delivery failed (%w) — the box's launcher fails closed without them", err)
	}
	fmt.Fprintln(warn, credDeliveredLine(time.Since(epoch)))
	return nil
}

// credDeliveredLine is the one owner of the delivery-honesty rule: a plain
// "delivered" only when the inject provably landed inside the launcher's
// wait; past the threshold byre cannot know whether the box's own wait
// already expired and failed it closed, and says so.
func credDeliveredLine(elapsed time.Duration) string {
	if elapsed <= credLateThreshold {
		return "byre: credentials: delivered."
	}
	return "byre: credentials: delivered late — the box's own bounded wait may already have expired and failed the launch closed (re-run byre develop)."
}

// stopCredentialsClosed ends a session whose credentials never landed. The
// box's own launcher fails closed on the missing sentinel, so this is not
// what makes the launch safe — it is what makes it PROMPT and legible: the
// user sees byre's cause instead of watching an attached box exit on a
// timeout of its own.
func stopCredentialsClosed(r sessionRunner, warn io.Writer, container string) {
	fmt.Fprintln(warn, "byre: stopping the box — declared credentials could not be delivered, and its launcher will not start the agent without them (failing closed).")
	if err := r.Stop(container); err != nil {
		fmt.Fprintf(warn, "byre: stopping the box failed: %v — its own bounded wait ends the launch instead.\n", err)
	}
}

// credentialRowCount is the exposure tally's credential segment: the rows
// the cascade would deliver, which is what a launch actually hands the box.
func credentialRowCount(groups []config.CredentialFile) int {
	return countRows(groups)
}
