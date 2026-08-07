package commands

// The launch side of project credentials (ADR 0057):
// the pre-lock unlock prompt, the under-lock read-once decrypt, and the
// post-start exec-stdin inject onto the session tmpfs. Everything here is
// degrade-never-block: a declined, absent, non-TTY, or failed unlock — and
// a delivery that never lands — all launch the box without credentials,
// with a notice, at no cost beyond the launcher's bounded fail-open wait.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	xterm "github.com/charmbracelet/x/term"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

const (
	// credTmpfsTarget is the session tmpfs the receiver writes and the
	// launcher reads — an ADR 0052 managed path.
	credTmpfsTarget = "/run/byre"
	// credPassphraseAttempts bounds the wrong-passphrase re-prompt (v13
	// pin: three attempts, Enter skips at any point).
	credPassphraseAttempts = 3
	// credInjectRunningWait bounds the poll for the container to be RUNNING.
	// It does not race the launcher: the launcher's own wait clock only
	// starts once the box starts, which is exactly when this poll ends.
	credInjectRunningWait = 60 * time.Second
	// credInjectDeadline bounds the exec itself, and THIS one is pinned
	// under the launcher's wait (BYRE_CRED_WAIT, default 20s in
	// launcher.sh): both clocks start at box start, so an exec that
	// completes inside this deadline lands before the launcher's fail-open
	// expires — "delivered" then honestly means the exports happen. The
	// margin absorbs the launcher's pre-wait lines; a stream is a few MiB
	// at most, so a full deadline spent is a wedged daemon, and the box
	// simply runs without credentials (the benign direction). A test pins
	// deadline + margin <= the launcher default so the two cannot drift
	// apart silently (the handoff's wait >= deadline + skew pin).
	credInjectDeadline = 15 * time.Second
	// credLateThreshold decides which delivery line is honest. The inject
	// goroutine's start precedes StartAttach, so elapsed-since-entry always
	// OVERESTIMATES elapsed-since-box-start — an inject that completes
	// inside this threshold is therefore PROVABLY inside the launcher's
	// wait (default 20s), and only then does byre say a plain "delivered".
	// Anything slower gets the hedge: the values are on the tmpfs, but the
	// box may already have launched without the exports. Measurement, not
	// clock-epoch assumptions — a slow engine cannot make byre lie.
	credLateThreshold = 18 * time.Second
)

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

// unlockResult is what the pre-lock prompt hands the locked phase: an
// unwrapped identity, or the outcome that explains its absence.
type unlockResult struct {
	u *credentials.Unlocked
	// outcome is set when u is nil: skipped-declined, skipped-nontty, or
	// unlock-failed. A nil unlockResult altogether means nothing was
	// declared pre-lock (no prompt ran).
	outcome credentials.Outcome
}

// promptCredentialUnlock is launch step 1 (pre-lock, TTY-only): enumerate
// the declared set with per-name value-state, then ask for the passphrase.
// The declarations are the standing cascade-visible consent to the set; the
// passphrase entry is both authentication and the per-launch consent act.
// The expensive scrypt unwrap happens HERE, before any lock — holding the
// setup lock across it would stall sibling worktrees for an authentication
// cost. Wrong passphrase re-prompts (three attempts total, Enter skips);
// everything else degrades to a launch without credentials.
func promptCredentialUnlock(s Streams, paths project.Paths, decls []config.CredentialDecl) *unlockResult {
	if len(decls) == 0 {
		return nil
	}
	vault := credentials.Open(paths.Dir, paths.ID)
	if !s.TTY {
		// Machine-readable: scripts watching stderr get one stable token.
		fmt.Fprintln(s.Err, "byre: credentials: skipped-nontty (no terminal for the unlock prompt) — launching without credentials.")
		return &unlockResult{outcome: credentials.OutcomeSkippedNonTTY}
	}
	if !vault.Exists() {
		fmt.Fprintln(s.Err, "byre: credentials are declared but this project has no vault — launching without them. Create one: byre credentials init")
		return &unlockResult{outcome: credentials.OutcomeUnlockFailed}
	}
	fmt.Fprintf(s.Err, "byre: credentials: %s\n", enumerateCredentials(decls, vault.EntryNames()))
	for attempt := 1; attempt <= credPassphraseAttempts; attempt++ {
		pw, err := readPassphrase(s.Err, "byre: passphrase to unlock credentials for this launch (Enter to skip): ")
		if err != nil {
			fmt.Fprintf(s.Err, "byre: credentials: could not read a passphrase (%v) — launching without credentials.\n", err)
			return &unlockResult{outcome: credentials.OutcomeSkippedNonTTY}
		}
		if pw == "" {
			fmt.Fprintln(s.Err, "byre: credentials: skipped — launching without them.")
			return &unlockResult{outcome: credentials.OutcomeSkippedDeclined}
		}
		u, err := vault.Unlock(pw)
		if err == nil {
			return &unlockResult{u: u}
		}
		if errors.Is(err, credentials.ErrBadPassphrase) && attempt < credPassphraseAttempts {
			fmt.Fprintln(s.Err, "byre: credentials: wrong passphrase — try again, or Enter to skip.")
			continue
		}
		fmt.Fprintf(s.Err, "byre: credentials: unlock failed (%v) — launching without credentials.\n", err)
		return &unlockResult{outcome: credentials.OutcomeUnlockFailed}
	}
	return &unlockResult{outcome: credentials.OutcomeUnlockFailed}
}

// enumerateCredentials renders the prompt's set line: every declared name
// with kind, target, and value-state (set/unset — the vault's word, from
// the entries dir, never the display cache).
func enumerateCredentials(decls []config.CredentialDecl, stored []string) string {
	has := map[string]bool{}
	for _, n := range stored {
		has[n] = true
	}
	parts := make([]string, 0, len(decls))
	for _, d := range decls {
		parts = append(parts, fmt.Sprintf("%s (%s → %s, %s)", d.Name, d.Kind, d.Target, credentials.ValueState(has[d.Name])))
	}
	return fmt.Sprintf("%d declared — %s", len(decls), strings.Join(parts, ", "))
}

// credPayload is the under-lock decrypt's product: what the inject streams,
// what the launch record notes, and what the chassis env/tmpfs need.
type credPayload struct {
	values   map[string][]byte // name -> plaintext, held only until the inject
	manifest string            // the launcher's export map: "name kind target" lines
	record   []launchCredential
	unlock   string // the record's unlock word (see launchCredentialUnlock*)
}

// The record's unlock vocabulary. "unlocked" and "not-prompted" are
// record-only words (the outcome vocabulary covers the skip/fail cases);
// not-prompted is the honest answer when a save landed credentials into the
// config while develop waited for the lock — there was no prompt to give.
const (
	launchCredentialUnlocked    = "unlocked"
	launchCredentialNotPrompted = "not-prompted"
)

// decryptCredentialsLocked is launch step 2, run under the setup lock with
// the AUTHORITATIVE declarations: with the identity already unwrapped, only
// the cheap per-entry decrypts hold the lock, each ciphertext read ONCE —
// the delivered bytes are those present at decrypt time (no snapshot, no
// hashes; plain correctness against a concurrent cooperating worktree).
// Per-name outcomes report here, where re-entry is actionable. A nil unlock
// (declarations appeared after the prompt window) and every skip/fail
// outcome produce an empty deliverable set: the launch proceeds without.
func decryptCredentialsLocked(w io.Writer, decls []config.CredentialDecl, unlock *unlockResult) credPayload {
	p := credPayload{values: map[string][]byte{}}
	if len(decls) == 0 {
		return p
	}
	if unlock == nil {
		fmt.Fprintln(w, "byre: credentials were declared while develop waited for the setup lock — no unlock was prompted; launching without them (re-run byre develop to unlock).")
		p.unlock = launchCredentialNotPrompted
		return p
	}
	if unlock.u == nil {
		p.unlock = string(unlock.outcome)
		return p
	}
	p.unlock = launchCredentialUnlocked
	var manifest strings.Builder
	kinds := map[string]string{}
	for _, d := range decls {
		value, outcome, err := unlock.u.Decrypt(d.Name)
		if err != nil {
			fmt.Fprintf(w, "byre: credentials: %s: %s (%v)\n", d.Name, outcome, err)
			p.record = append(p.record, launchCredential{Name: d.Name, Kind: d.Kind, Target: d.Target, Outcome: string(outcome)})
			continue
		}
		// Hold the value to its declared kind here, at delivery composition:
		// a cold write may have predated the declaration (kind unknown at
		// save), so save-time validation alone cannot cover this.
		if verr := credentials.ValidateValue(value, d.Kind); verr != nil {
			fmt.Fprintf(w, "byre: credentials: %s: %s (%v)\n", d.Name, credentials.OutcomeUnsupportedFormat, verr)
			p.record = append(p.record, launchCredential{Name: d.Name, Kind: d.Kind, Target: d.Target, Outcome: string(credentials.OutcomeUnsupportedFormat)})
			continue
		}
		p.values[d.Name] = value
		kinds[d.Name] = d.Kind
		fmt.Fprintf(&manifest, "%s %s %s\n", d.Name, d.Kind, d.Target)
		p.record = append(p.record, launchCredential{Name: d.Name, Kind: d.Kind, Target: d.Target, Outcome: launchCredentialScheduled})
	}
	p.manifest = manifest.String()
	// Display-cache upkeep (best-effort, never load-bearing).
	unlock.u.RepairIndex(kinds)
	if len(p.values) == 0 {
		fmt.Fprintln(w, "byre: credentials: nothing deliverable — launching without them.")
	}
	return p
}

// launchCredentialScheduled is the record's word for a value that decrypted
// and was scheduled for delivery. Deliberately not "delivered": the record
// is written (content-addressed, immutable) before the container starts,
// and the design does not probe in-box state afterwards — the inject's own
// delivered/not-delivered outcome is reported on stderr as it happens.
const launchCredentialScheduled = "scheduled"

// credStream frames the deliverable set for the receiver: a version line,
// the manifest first, values in sorted order, "done" last. Payloads ride
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
	names := make([]string, 0, len(p.values))
	for n := range p.values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		frame(n, p.values[n])
	}
	b.WriteString("done\n")
	return b.Bytes()
}

// credTmpfs sizes the session tmpfs for the deliverable set: the payload
// plus fixed headroom, so an fs-metadata surprise never truncates a value.
func credTmpfs(p credPayload, ident runner.Identity, eng runner.Engine) runner.TmpfsMount {
	var total int64
	for _, v := range p.values {
		total += int64(len(v))
	}
	total += int64(len(p.manifest))
	return runner.TmpfsMount{
		Target: credTmpfsTarget,
		Size:   total + (1 << 20),
		Mode:   "0700",
		UID:    ident.UID,
		GID:    ident.GID,
		// Podman copies image content up into a fresh tmpfs; a byre-owned
		// delivery mount never wants that.
		NoCopyUp: eng == runner.Podman,
	}
}

// credentialUnlockLine renders the record's unlock word for the status
// Credentials row: launch-time facts, never live-state claims (byre does
// not probe the box).
func credentialUnlockLine(unlock string) string {
	switch unlock {
	case launchCredentialUnlocked:
		return "unlocked (credentials were decrypted and handed to delivery)"
	case launchCredentialNotPrompted:
		return "not-prompted (credentials were declared while develop waited for the lock)"
	default:
		return unlock + " (launched without credentials)"
	}
}

// runCredentialInject is launch step 3's host side, run concurrently with
// the attached session (the netns-hook pattern): wait for the box to be
// RUNNING (exec needs a live container), then pipe the framed stream to the
// baked receiver as the dev identity, bounded. Every failure is fail-open —
// reported, never blocking the box, which simply runs without credentials
// when its launcher's own wait expires.
func runCredentialInject(r sessionRunner, warn io.Writer, label, containerID string, ident runner.Identity, stream []byte, epoch time.Time, done <-chan struct{}) {
	// epoch was captured by develop SYNCHRONOUSLY before this goroutine was
	// spawned (and so before StartAttach could run): elapsed-since-epoch
	// bounds elapsed-since-box-start from above by construction, which is
	// what makes the delivered/late line honest (see credLateThreshold).
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	deadline := epoch.Add(credInjectRunningWait)
	running := false
	for !running {
		if ids, err := r.RunningContainersByLabel(label); err == nil && len(ids) > 0 {
			running = true
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintln(warn, "byre: credentials: not-delivered (the box never reached running within the wait) — it runs without credentials.")
			return
		}
		select {
		case <-done:
			return // session over before the box ran; nothing to deliver to
		case <-tick.C:
		}
	}
	if _, err := r.ExecInputBounded(credInjectDeadline, containerID, ident.UID, ident.GID, bytes.NewReader(stream), gen.ReceiverPath); err != nil {
		fmt.Fprintf(warn, "byre: credentials: not-delivered (%v) — the box runs without credentials.\n", err)
		return
	}
	fmt.Fprintln(warn, credDeliveredLine(time.Since(epoch)))
}

// credDeliveredLine is the one owner of the delivery-honesty rule: a plain
// "delivered" only when the inject provably landed inside the launcher's
// wait; past the threshold byre cannot know whether the exports happened
// and says so (the values still sit on the session tmpfs the agent owns).
func credDeliveredLine(elapsed time.Duration) string {
	if elapsed <= credLateThreshold {
		return "byre: credentials: delivered."
	}
	return "byre: credentials: delivered late — the box may have launched without the env exports (the values are on the session tmpfs; restart the agent process to pick them up, or re-run byre develop)."
}
