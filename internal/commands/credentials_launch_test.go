package commands

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

func init() {
	// Production's pinned scrypt cost is deliberate at the prompt and pure
	// drag in a test suite (the unwrap path is identical at any logN).
	credentials.SetWorkFactorForTesting(10)
}

// credGroup builds one contributing cascade file: its own identity under
// passphrase, and one encrypted row per key.
func credGroup(t *testing.T, label, passphrase string, rows map[string]string) config.CredentialFile {
	t.Helper()
	wrapped, recipient, err := credentials.NewIdentity(passphrase)
	if err != nil {
		t.Fatal(err)
	}
	g := config.CredentialFile{
		Label:    label,
		Path:     "/home/u/.byre/" + strings.Replace(label, ":", "-", 1) + ".config",
		Block:    config.CredentialsBlock{Identity: wrapped, Recipient: recipient},
		HasBlock: true,
	}
	for _, k := range sortedKeys(rows) {
		blob, err := credentials.EncryptValue(recipient, k, credentials.KindEnv, []byte(rows[k]))
		if err != nil {
			t.Fatal(err)
		}
		g.Rows = append(g.Rows, config.EncryptedRow{Key: k, Kind: credentials.KindEnv, Blob: blob})
	}
	return g
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// credResolved is the resolved view a launch reads: an empty config plus the
// cascade's credential groups, which is all the launch flow consumes.
func credResolved(groups ...config.CredentialFile) resolved {
	rv := combine(config.Config{}, skills.Resolved{})
	rv.credFiles = groups
	return rv
}

// credFixture: a bootstrapped project and one project-file group holding one
// env credential.
func credFixture(t *testing.T) (project.Paths, resolved) {
	t.Helper()
	p, _ := testPaths(t)
	return p, credResolved(credGroup(t, "project", "pw", map[string]string{"STRIPE_KEY": "sk-live-123"}))
}

// passphraseSeam answers the masked prompt with the given lines, recording
// how often it was asked and what it was asked for.
func passphraseSeam(t *testing.T, answers ...string) (*int, *[]string) {
	t.Helper()
	old := readPassphrase
	t.Cleanup(func() { readPassphrase = old })
	calls := 0
	var prompts []string
	readPassphrase = func(w io.Writer, prompt string) (string, error) {
		if calls >= len(answers) {
			t.Fatalf("passphrase prompt asked %d times, only %d answers provided", calls+1, len(answers))
		}
		a := answers[calls]
		calls++
		prompts = append(prompts, prompt)
		return a, nil
	}
	return &calls, &prompts
}

func ttyStreams(errBuf *bytes.Buffer) Streams {
	return Streams{Out: io.Discard, Err: errBuf, In: strings.NewReader(""), TTY: true}
}

func TestDevelopDeliversCredentials(t *testing.T) {
	p, rv := credFixture(t)
	passphraseSeam(t, "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{
		// Empty on the fast-path check; running from the second query on, so
		// the inject's poll sees the box live.
		liveSecond: map[string][]string{workdirLabel(p): {"cid-live"}},
	}
	if err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(f.creates[0], " ")
	if !strings.Contains(argv, "--tmpfs /run/byre:rw,noexec,nosuid,nodev,mode=0700,uid="+fmt.Sprint(os.Getuid())) {
		t.Fatalf("create argv missing the session tmpfs: %s", argv)
	}
	if !strings.Contains(argv, "-e BYRE_CRED_EXPECT=1") {
		t.Fatalf("create argv missing the wait/export flag: %s", argv)
	}
	// An encrypted row is delivered ONLY on the tmpfs channel: it must never
	// also ride the ordinary env_from_host -e export.
	if strings.Contains(argv, "STRIPE_KEY=") {
		t.Fatalf("a credential row must never reach the engine argv: %s", argv)
	}
	// The inject went to the created container as the dev identity, into the
	// baked receiver, carrying manifest + value (base64-framed) under the
	// CONFIG KEY.
	inject := ""
	for _, e := range f.execInputs {
		if strings.Contains(e, "bounded") {
			inject = e
		}
	}
	if inject == "" || !strings.Contains(inject, "fake-container-id") || !strings.Contains(inject, gen.ReceiverPath) {
		t.Fatalf("no bounded inject to the receiver recorded: %v", f.execInputs)
	}
	if !strings.Contains(inject, "item STRIPE_KEY") || !strings.Contains(inject, base64.StdEncoding.EncodeToString([]byte("sk-live-123"))) {
		t.Fatalf("inject stream missing the framed value: %q", inject)
	}
	if !strings.Contains(inject, base64.StdEncoding.EncodeToString([]byte("STRIPE_KEY env\n"))) {
		t.Fatalf("inject stream missing the manifest frame: %q", inject)
	}
	if out := errBuf.String(); !strings.Contains(out, "credentials: delivered") {
		t.Fatalf("delivery not reported: %s", out)
	}
	// The launch record carries the launch-time facts: unlock outcome and
	// per-row decrypt outcomes. Keys and outcomes only — never values.
	recs, err := filepath.Glob(filepath.Join(p.Dir, "launches", "*.toml"))
	if err != nil || len(recs) != 1 {
		t.Fatalf("launch records: %v %v", recs, err)
	}
	rec, err := os.ReadFile(recs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"credential_unlock = 'unlocked'", "key = 'STRIPE_KEY'", "source = 'project'", "outcome = 'scheduled'"} {
		if !strings.Contains(string(rec), want) {
			t.Fatalf("launch record missing %q:\n%s", want, rec)
		}
	}
	if strings.Contains(string(rec), "sk-live-123") {
		t.Fatal("launch record must never carry a credential value")
	}
}

// The plan line comes first, so a user knows how many passphrases they are
// about to be asked for and which files they belong to.
func TestDevelopPlanLineNamesEveryFile(t *testing.T) {
	p, _ := testPaths(t)
	rv := credResolved(
		credGroup(t, "default", "pw", map[string]string{"A": "1", "B": "2"}),
		credGroup(t, "layer:acme", "pw", map[string]string{"C": "3"}),
	)
	passphraseSeam(t, "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{liveSecond: map[string][]string{workdirLabel(p): {"cid"}}}
	if err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "unlocking credentials: default (2), layer acme (1)") {
		t.Fatalf("plan line: %s", errBuf.String())
	}
}

// People reuse passphrases across their own files: an entered one is tried on
// every still-locked identity before anybody is asked a second time.
func TestDevelopReusesOnePassphraseAcrossFiles(t *testing.T) {
	p, _ := testPaths(t)
	rv := credResolved(
		credGroup(t, "default", "same", map[string]string{"A": "1"}),
		credGroup(t, "project", "same", map[string]string{"B": "2"}),
	)
	calls, prompts := passphraseSeam(t, "same")
	var errBuf bytes.Buffer
	f := &fakeRunner{liveSecond: map[string][]string{workdirLabel(p): {"cid"}}}
	if err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("prompted %d times for one shared passphrase: %v", *calls, *prompts)
	}
	// Root-most first: the one prompt names the file it started with.
	if !strings.Contains((*prompts)[0], "default") {
		t.Fatalf("prompts must run root-most first: %q", (*prompts)[0])
	}
}

// Distinct passphrases are asked for in merge order, root-most first.
func TestDevelopPromptsPerFileRootMostFirst(t *testing.T) {
	p, _ := testPaths(t)
	rv := credResolved(
		credGroup(t, "layer:acme", "layer-pw", map[string]string{"A": "1"}),
		credGroup(t, "project", "proj-pw", map[string]string{"B": "2"}),
	)
	_, prompts := passphraseSeam(t, "layer-pw", "proj-pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{liveSecond: map[string][]string{workdirLabel(p): {"cid"}}}
	if err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk); err != nil {
		t.Fatal(err)
	}
	if len(*prompts) != 2 || !strings.Contains((*prompts)[0], "layer acme") || !strings.Contains((*prompts)[1], "project") {
		t.Fatalf("prompt order: %v", *prompts)
	}
}

// Blocking: a wrong passphrase after its attempts stops the launch. Nothing
// is created, and the refusal names the file and a remedy.
func TestDevelopWrongPassphraseStopsTheLaunch(t *testing.T) {
	p, rv := credFixture(t)
	calls, _ := passphraseSeam(t, "bad1", "bad2", "bad3")
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk)
	if err == nil || !strings.Contains(err.Error(), "wrong passphrase for project") ||
		!strings.Contains(err.Error(), "--credentials=skip") {
		t.Fatalf("want a stop naming the file and a remedy, got: %v", err)
	}
	// The remedy must be one this user can actually run: `rekey` opens by
	// asking for the passphrase they have just lost.
	if !strings.Contains(err.Error(), "rekey` cannot help") ||
		!strings.Contains(err.Error(), "byre credentials unset STRIPE_KEY") {
		t.Fatalf("the remedy must not point at rekey: %v", err)
	}
	if *calls != credPassphraseAttempts {
		t.Fatalf("attempts = %d, want %d", *calls, credPassphraseAttempts)
	}
	if len(f.creates) != 0 {
		t.Fatalf("a stopped launch must create nothing: %v", f.creates)
	}
}

// An empty passphrase is not a skip any more — it is worthless input, said so
// and re-asked.
func TestDevelopEmptyPassphraseIsRejectedNotASkip(t *testing.T) {
	p, rv := credFixture(t)
	passphraseSeam(t, "", "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{liveSecond: map[string][]string{workdirLabel(p): {"cid"}}}
	if err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), credentials.EmptyPassphraseWorthless) {
		t.Fatalf("empty-passphrase notice: %s", errBuf.String())
	}
	if argv := strings.Join(f.creates[0], " "); !strings.Contains(argv, "BYRE_CRED_EXPECT") {
		t.Fatalf("the second, real passphrase must still deliver: %s", argv)
	}
}

// develop reads one cascade TWICE (config.Load for the merged config,
// config.CascadeFiles for the per-file credential view), and a layer written
// between the reads can leave a key in the first and not the second. Nothing
// downstream reports that: an encrypted row never joins the -e export, so the
// box would launch without the value and byre would say nothing. The
// disagreement is constructed here directly — racing the two reads would test
// the scheduler, not the check.
func TestDevelopRefusesWhenTheTwoCascadeViewsDisagree(t *testing.T) {
	row, err := config.FormatEncryptedRow(credentials.KindEnv, []byte("ciphertext"))
	if err != nil {
		t.Fatal(err)
	}
	// The credential file view carries STRIPE_KEY only; the merged config
	// carries GHOST_KEY as an encrypted row too.
	mismatched := func(t *testing.T) (project.Paths, resolved) {
		p, rv := credFixture(t)
		rv.cfg.EnvFromHost = map[string]string{"STRIPE_KEY": row, "GHOST_KEY": row}
		return p, rv
	}

	p, rv := mismatched(t)
	passphraseSeam(t, "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{liveSecond: map[string][]string{workdirLabel(p): {"cid"}}}
	err = develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk)
	if err == nil || !strings.Contains(err.Error(), "GHOST_KEY") ||
		!strings.Contains(err.Error(), "the two reads of the cascade disagree") {
		t.Fatalf("want a refusal naming the key and the two views, got: %v", err)
	}
	if strings.Contains(err.Error(), "STRIPE_KEY") {
		t.Fatalf("only the key the views disagree over may be named: %v", err)
	}
	if len(f.creates) != 0 {
		t.Fatalf("a refused launch must create nothing: %v", f.creates)
	}

	// A deliberate skip is not silent about launching without the values, so
	// the disagreement changes nothing it would have delivered.
	p, rv = mismatched(t)
	f = &fakeRunner{}
	if err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialSkip); err != nil {
		t.Fatalf("--credentials=skip must still launch: %v", err)
	}

	// The agreeing shape still launches — a check that fired on every config
	// carrying a credential row would pass the refusal above for free.
	p, rv = credFixture(t)
	rv.cfg.EnvFromHost = map[string]string{"STRIPE_KEY": row}
	passphraseSeam(t, "pw")
	f = &fakeRunner{liveSecond: map[string][]string{workdirLabel(p): {"cid"}}}
	if err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk); err != nil {
		t.Fatalf("the views agree, so the launch must proceed: %v", err)
	}
	if len(f.creates) == 0 {
		t.Fatal("the agreeing launch created no container")
	}
}

// --credentials=skip is the ONE deliberate way to launch without them, and
// the record says which it was.
func TestDevelopSkipModeLaunchesWithout(t *testing.T) {
	p, rv := credFixture(t)
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	if err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialSkip); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(f.creates[0], " ")
	if strings.Contains(argv, "--tmpfs") || strings.Contains(argv, "BYRE_CRED_EXPECT") {
		t.Fatalf("a skipped launch must not arm delivery: %s", argv)
	}
	for _, e := range f.execInputs {
		if strings.Contains(e, "bounded") {
			t.Fatalf("no inject may run on a skipped launch: %v", f.execInputs)
		}
	}
	recs, _ := filepath.Glob(filepath.Join(p.Dir, "launches", "*.toml"))
	rec, _ := os.ReadFile(recs[0])
	if !strings.Contains(string(rec), "credential_unlock = 'skipped-declined'") {
		t.Fatalf("record unlock word:\n%s", rec)
	}
}

// No terminal to prompt on is a STOP with the two ways out, not a silent
// launch without the values.
func TestDevelopNonTTYStopsWithRemedies(t *testing.T) {
	p, rv := credFixture(t)
	var errBuf bytes.Buffer
	s := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader(""), TTY: false}
	f := &fakeRunner{}
	err := develop(f, s, p, rv, false, CredentialAsk)
	if err == nil || !strings.Contains(err.Error(), "no terminal") ||
		!strings.Contains(err.Error(), "--credentials=stdin") || !strings.Contains(err.Error(), "--credentials=skip") {
		t.Fatalf("want a stop naming both remedies, got: %v", err)
	}
	if len(f.creates) != 0 {
		t.Fatalf("a stopped launch must create nothing: %v", f.creates)
	}
}

// --credentials=stdin: one passphrase per line, each tried against every
// still-locked identity in file order.
func TestDevelopStdinMode(t *testing.T) {
	p, _ := testPaths(t)
	rv := credResolved(
		credGroup(t, "layer:acme", "layer-pw", map[string]string{"A": "1"}),
		credGroup(t, "project", "proj-pw", map[string]string{"B": "2"}),
	)
	var errBuf bytes.Buffer
	s := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("proj-pw\nlayer-pw\n"), TTY: false}
	f := &fakeRunner{liveSecond: map[string][]string{workdirLabel(p): {"cid"}}}
	if err := develop(f, s, p, rv, false, CredentialStdin); err != nil {
		t.Fatal(err)
	}
	if argv := strings.Join(f.creates[0], " "); !strings.Contains(argv, "BYRE_CRED_EXPECT") {
		t.Fatalf("stdin passphrases must deliver: %s", argv)
	}
}

func TestDevelopStdinEOFWithLockedFilesStops(t *testing.T) {
	p, rv := credFixture(t)
	var errBuf bytes.Buffer
	s := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("nope\n"), TTY: false}
	f := &fakeRunner{}
	err := develop(f, s, p, rv, false, CredentialStdin)
	if err == nil || !strings.Contains(err.Error(), "stdin ended with 1 file(s) still locked") ||
		!strings.Contains(err.Error(), "project") {
		t.Fatalf("want a stop naming the still-locked file, got: %v", err)
	}
	if len(f.creates) != 0 {
		t.Fatalf("a stopped launch must create nothing: %v", f.creates)
	}
}

// A row copied without the identity that opens it: refused before a single
// passphrase prompt, and never opened with a neighbouring file's block.
func TestDevelopRowWithoutABlockStops(t *testing.T) {
	p, _ := testPaths(t)
	g := credGroup(t, "project", "pw", map[string]string{"STRIPE_KEY": "v"})
	g.HasBlock = false
	g.Block = config.CredentialsBlock{}
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	err := develop(f, ttyStreams(&errBuf), p, credResolved(g), false, CredentialAsk)
	if err == nil || !strings.Contains(err.Error(), "no [credentials] block") ||
		!strings.Contains(err.Error(), "STRIPE_KEY") {
		t.Fatalf("want a stop naming the file and the row, got: %v", err)
	}
	if len(f.creates) != 0 {
		t.Fatalf("a stopped launch must create nothing: %v", f.creates)
	}
}

// The payload's key/kind stamp is an accident guard, and it stops the launch
// rather than delivering the wrong value under the right name.
func TestDevelopMismatchedBlobStops(t *testing.T) {
	p, _ := testPaths(t)
	g := credGroup(t, "project", "pw", map[string]string{"STRIPE_KEY": "v"})
	// The blob is stamped for STRIPE_KEY; the row now claims another key.
	g.Rows[0].Key = "GH_TOKEN"
	passphraseSeam(t, "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	err := develop(f, ttyStreams(&errBuf), p, credResolved(g), false, CredentialAsk)
	if err == nil || !strings.Contains(err.Error(), string(credentials.OutcomeRowMismatch)) ||
		!strings.Contains(err.Error(), "GH_TOKEN") {
		t.Fatalf("want a stop naming the mismatch and the key, got: %v", err)
	}
	if len(f.creates) != 0 {
		t.Fatalf("a stopped launch must create nothing: %v", f.creates)
	}
}

// A cascade whose credential rows cannot be read at all stops the launch —
// blocking has no "read what parsed" arm.
func TestDevelopUnreadableCascadeStops(t *testing.T) {
	p, _ := testPaths(t)
	rv := combine(config.Config{}, skills.Resolved{})
	rv.credErr = fmt.Errorf("layer:acme: env_from_host A: the encrypted value is not valid base64 (!!)")
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk)
	if err == nil || !strings.Contains(err.Error(), "not valid base64") {
		t.Fatalf("want the cascade refusal surfaced, got: %v", err)
	}
	if len(f.creates) != 0 {
		t.Fatalf("a stopped launch must create nothing: %v", f.creates)
	}
}

// Delivery that never lands is a failed LAUNCH: byre stops the box (its
// launcher would fail closed anyway) and reports the real cause.
func TestInjectFailureStopsTheBoxAndFailsTheLaunch(t *testing.T) {
	p, rv := credFixture(t)
	passphraseSeam(t, "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{
		liveSecond:          map[string][]string{workdirLabel(p): {"cid"}},
		execInputBoundedErr: fmt.Errorf("exec: container gone"),
	}
	err := develop(f, ttyStreams(&errBuf), p, rv, false, CredentialAsk)
	if err == nil || !strings.Contains(err.Error(), "delivery failed") {
		t.Fatalf("want the launch to fail with the delivery cause, got: %v", err)
	}
	if len(f.stops) == 0 {
		t.Fatal("a failed delivery must stop the box rather than leave it running credless")
	}
}

// TestInjectDeadlineUnderLauncherWait pins the delivery-honesty clocks
// against the launcher script itself so they cannot drift apart silently:
// a plain "delivered" is only claimed when the inject landed inside
// credLateThreshold, whose measurement OVERESTIMATES time-since-box-start
// (the goroutine's entry precedes StartAttach) — so the threshold must sit
// strictly inside the launcher's wait, and the exec deadline inside the
// threshold (a max-length successful exec must still be able to earn the
// plain word).
func TestInjectDeadlineUnderLauncherWait(t *testing.T) {
	m := regexp.MustCompile(`BYRE_CRED_WAIT:-(\d+)`).FindSubmatch(gen.LauncherScript())
	if m == nil {
		t.Fatal("launcher.sh no longer defaults BYRE_CRED_WAIT — update this pin")
	}
	waitSecs, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	launcherWait := time.Duration(waitSecs) * time.Second
	if credLateThreshold >= launcherWait {
		t.Fatalf("credLateThreshold %v must sit inside the launcher's %v wait — past it byre cannot know the exports happened, and the plain 'delivered' would be a guess", credLateThreshold, launcherWait)
	}
	if credInjectDeadline > credLateThreshold {
		t.Fatalf("credInjectDeadline %v exceeds credLateThreshold %v — even a successful max-length exec could never honestly report plain delivery", credInjectDeadline, credLateThreshold)
	}
}

// The honesty rule itself, at its one owner: inside the threshold the plain
// word; past it the hedge — byre does not probe the box, so it never claims
// an export it cannot know happened.
func TestCredDeliveredLineHonesty(t *testing.T) {
	if got := credDeliveredLine(2 * time.Second); got != "byre: credentials: delivered." {
		t.Fatalf("in-window line = %q", got)
	}
	late := credDeliveredLine(credLateThreshold + time.Second)
	if !strings.Contains(late, "delivered late") || !strings.Contains(late, "failed the launch closed") {
		t.Fatalf("past-window line must hedge: %q", late)
	}
}

// The stream framing is a CONTRACT with credential-receiver.sh: a version
// line, the manifest first, values in key order, "done" last.
func TestCredStreamFraming(t *testing.T) {
	p := credPayload{
		values:   map[string][]byte{"B_KEY": []byte("two"), "A_KEY": []byte("one")},
		manifest: "A_KEY env\nB_KEY file\n",
	}
	got := string(credStream(p))
	want := "byre-credentials 1\n" +
		"item manifest\n" + base64.StdEncoding.EncodeToString([]byte("A_KEY env\nB_KEY file\n")) + "\n" +
		"item A_KEY\n" + base64.StdEncoding.EncodeToString([]byte("one")) + "\n" +
		"item B_KEY\n" + base64.StdEncoding.EncodeToString([]byte("two")) + "\n" +
		"done\n"
	if got != want {
		t.Fatalf("stream:\n%q\nwant:\n%q", got, want)
	}
}

func TestParseCredentialMode(t *testing.T) {
	for in, want := range map[string]CredentialMode{"": CredentialAsk, "ask": CredentialAsk, "skip": CredentialSkip, "stdin": CredentialStdin} {
		got, err := ParseCredentialMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseCredentialMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseCredentialMode("maybe"); err == nil || !strings.Contains(err.Error(), "want ask|skip|stdin") ||
		!strings.Contains(err.Error(), "maybe") {
		t.Fatalf("want a refusal naming the rule and the value: %v", err)
	}
}
