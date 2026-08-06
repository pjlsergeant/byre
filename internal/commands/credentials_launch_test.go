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

// credFixture: a bootstrapped project whose store holds a vault with one
// stored value, and a config declaring it (plus one declared-but-unset name).
func credFixture(t *testing.T) (project.Paths, config.Config) {
	t.Helper()
	p, _ := testPaths(t)
	v := credentials.Open(p.Dir, p.ID)
	if err := v.Create("pw"); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("stripe", []byte("sk-live-123"), "env"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Credentials: []config.CredentialDecl{
		{Name: "stripe", Kind: "env", Target: "STRIPE_KEY"},
		{Name: "github", Kind: "env", Target: "GH_TOKEN"},
	}}
	return p, cfg
}

// passphraseSeam answers the masked prompt with the given lines, recording
// how often it was asked.
func passphraseSeam(t *testing.T, answers ...string) *int {
	t.Helper()
	old := readPassphrase
	t.Cleanup(func() { readPassphrase = old })
	calls := 0
	readPassphrase = func(w io.Writer, prompt string) (string, error) {
		if calls >= len(answers) {
			t.Fatalf("passphrase prompt asked %d times, only %d answers provided", calls+1, len(answers))
		}
		a := answers[calls]
		calls++
		return a, nil
	}
	return &calls
}

func ttyStreams(errBuf *bytes.Buffer) Streams {
	return Streams{Out: io.Discard, Err: errBuf, In: strings.NewReader(""), TTY: true}
}

func TestDevelopDeliversCredentials(t *testing.T) {
	p, cfg := credFixture(t)
	passphraseSeam(t, "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{
		// Empty on the fast-path check; running from the second query on, so
		// the inject's poll sees the box live.
		liveSecond: map[string][]string{workdirLabel(p): {"cid-live"}},
	}
	if err := develop(f, ttyStreams(&errBuf), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(f.creates[0], " ")
	if !strings.Contains(argv, "--tmpfs /run/byre:rw,noexec,nosuid,nodev,mode=0700,uid="+fmt.Sprint(os.Getuid())) {
		t.Fatalf("create argv missing the session tmpfs: %s", argv)
	}
	if !strings.Contains(argv, "-e BYRE_CRED_EXPECT=1") {
		t.Fatalf("create argv missing the wait/export flag: %s", argv)
	}
	// The inject went to the created container as the dev identity, into the
	// baked receiver, carrying manifest + value (base64-framed).
	inject := ""
	for _, e := range f.execInputs {
		if strings.Contains(e, "bounded") {
			inject = e
		}
	}
	if inject == "" || !strings.Contains(inject, "fake-container-id") || !strings.Contains(inject, gen.ReceiverPath) {
		t.Fatalf("no bounded inject to the receiver recorded: %v", f.execInputs)
	}
	if !strings.Contains(inject, "item stripe") || !strings.Contains(inject, base64.StdEncoding.EncodeToString([]byte("sk-live-123"))) {
		t.Fatalf("inject stream missing the framed value: %q", inject)
	}
	if !strings.Contains(inject, base64.StdEncoding.EncodeToString([]byte("stripe env STRIPE_KEY\n"))) {
		t.Fatalf("inject stream missing the manifest frame: %q", inject)
	}
	out := errBuf.String()
	// The declared-but-unset name degrades per name; the set one delivers.
	if !strings.Contains(out, "github: missing-value") {
		t.Fatalf("missing-value outcome not reported: %s", out)
	}
	if !strings.Contains(out, "credentials: delivered") {
		t.Fatalf("delivery not reported: %s", out)
	}
	// The launch record carries the launch-time facts: unlock outcome and
	// per-name decrypt outcomes. Names and outcomes only — never values.
	recs, err := filepath.Glob(filepath.Join(p.Dir, "launches", "*.toml"))
	if err != nil || len(recs) != 1 {
		t.Fatalf("launch records: %v %v", recs, err)
	}
	rec, err := os.ReadFile(recs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"credential_unlock = 'unlocked'", "name = 'stripe'", "outcome = 'scheduled'", "outcome = 'missing-value'"} {
		if !strings.Contains(string(rec), want) {
			t.Fatalf("launch record missing %q:\n%s", want, rec)
		}
	}
	if strings.Contains(string(rec), "sk-live-123") {
		t.Fatal("launch record must never carry a credential value")
	}
}

func TestDevelopNonTTYSkipsCredentials(t *testing.T) {
	p, cfg := credFixture(t)
	var errBuf bytes.Buffer
	s := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader(""), TTY: false}
	f := &fakeRunner{}
	if err := develop(f, s, p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(f.creates[0], " ")
	if strings.Contains(argv, "--tmpfs") || strings.Contains(argv, "BYRE_CRED_EXPECT") {
		t.Fatalf("non-TTY launch must not arm delivery: %s", argv)
	}
	// The stable machine-readable token, and the record's honest word.
	if !strings.Contains(errBuf.String(), "skipped-nontty") {
		t.Fatalf("skipped-nontty notice missing: %s", errBuf.String())
	}
	for _, e := range f.execInputs {
		if strings.Contains(e, "bounded") {
			t.Fatalf("no inject may run on a skipped launch: %v", f.execInputs)
		}
	}
	recs, _ := filepath.Glob(filepath.Join(p.Dir, "launches", "*.toml"))
	rec, _ := os.ReadFile(recs[0])
	if !strings.Contains(string(rec), "credential_unlock = 'skipped-nontty'") {
		t.Fatalf("record unlock word:\n%s", rec)
	}
}

func TestDevelopDeclinedUnlockSkips(t *testing.T) {
	p, cfg := credFixture(t)
	passphraseSeam(t, "") // Enter = skip
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	if err := develop(f, ttyStreams(&errBuf), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	if argv := strings.Join(f.creates[0], " "); strings.Contains(argv, "BYRE_CRED_EXPECT") {
		t.Fatalf("declined unlock must not arm delivery: %s", argv)
	}
	if !strings.Contains(errBuf.String(), "skipped — launching without") {
		t.Fatalf("decline notice: %s", errBuf.String())
	}
}

func TestDevelopWrongPassphraseBoundedRetry(t *testing.T) {
	p, cfg := credFixture(t)
	calls := passphraseSeam(t, "bad1", "bad2", "bad3")
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	if err := develop(f, ttyStreams(&errBuf), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	if *calls != 3 {
		t.Fatalf("attempts = %d, want exactly 3 (the bounded re-prompt)", *calls)
	}
	out := errBuf.String()
	if !strings.Contains(out, "wrong passphrase — try again") {
		t.Fatalf("re-prompt notice missing: %s", out)
	}
	if !strings.Contains(out, "unlock failed") || !strings.Contains(out, "launching without credentials") {
		t.Fatalf("exhausted attempts must degrade to a launch without: %s", out)
	}
	if argv := strings.Join(f.creates[0], " "); strings.Contains(argv, "BYRE_CRED_EXPECT") {
		t.Fatalf("failed unlock must not arm delivery: %s", argv)
	}
}

func TestDevelopWrongThenRightPassphrase(t *testing.T) {
	p, cfg := credFixture(t)
	passphraseSeam(t, "bad", "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{liveSecond: map[string][]string{workdirLabel(p): {"cid"}}}
	if err := develop(f, ttyStreams(&errBuf), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	if argv := strings.Join(f.creates[0], " "); !strings.Contains(argv, "BYRE_CRED_EXPECT") {
		t.Fatalf("a typo then the right passphrase must still deliver: %s", argv)
	}
}

func TestDevelopUnsetVaultPromptEnumerates(t *testing.T) {
	p, cfg := credFixture(t)
	passphraseSeam(t, "")
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	if err := develop(f, ttyStreams(&errBuf), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	out := errBuf.String()
	// The prompt enumerates the declared set with per-name value-state.
	if !strings.Contains(out, "stripe (env → STRIPE_KEY, set)") || !strings.Contains(out, "github (env → GH_TOKEN, unset)") {
		t.Fatalf("enumeration line: %s", out)
	}
}

func TestDevelopNoVaultDegrades(t *testing.T) {
	p, _ := testPaths(t) // no vault created
	cfg := config.Config{Credentials: []config.CredentialDecl{{Name: "a", Kind: "env", Target: "A"}}}
	var errBuf bytes.Buffer
	f := &fakeRunner{}
	if err := develop(f, ttyStreams(&errBuf), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "no vault") || !strings.Contains(errBuf.String(), "byre credentials init") {
		t.Fatalf("missing-vault notice with the remedy: %s", errBuf.String())
	}
	if argv := strings.Join(f.creates[0], " "); strings.Contains(argv, "BYRE_CRED_EXPECT") {
		t.Fatalf("no vault must not arm delivery: %s", argv)
	}
}

func TestRunCredentialInjectFailureReportsNotDelivered(t *testing.T) {
	p, cfg := credFixture(t)
	passphraseSeam(t, "pw")
	var errBuf bytes.Buffer
	f := &fakeRunner{
		liveSecond:          map[string][]string{workdirLabel(p): {"cid"}},
		execInputBoundedErr: fmt.Errorf("exec: container gone"),
	}
	if err := develop(f, ttyStreams(&errBuf), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "not-delivered") {
		t.Fatalf("inject failure must report not-delivered and never block: %s", errBuf.String())
	}
}

// TestInjectDeadlineUnderLauncherWait pins the two clocks that decide
// whether "delivered" means the exports happen: both start at box start, so
// the host exec deadline plus margin must sit INSIDE the launcher's
// fail-open wait — the handoff's wait >= deadline + skew pin. Parsed from
// the launcher script itself so the two cannot drift apart silently.
func TestInjectDeadlineUnderLauncherWait(t *testing.T) {
	m := regexp.MustCompile(`BYRE_CRED_WAIT:-(\d+)`).FindSubmatch(gen.LauncherScript())
	if m == nil {
		t.Fatal("launcher.sh no longer defaults BYRE_CRED_WAIT — update this pin")
	}
	waitSecs, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	const margin = 4 * time.Second // launcher pre-wait lines + poll granularity
	if launcherWait := time.Duration(waitSecs) * time.Second; credInjectDeadline+margin > launcherWait {
		t.Fatalf("credInjectDeadline %v + %v margin exceeds the launcher's %v wait — a successful inject could land after the box failed open, making 'delivered' dishonest", credInjectDeadline, margin, launcherWait)
	}
}

func TestCredStreamFraming(t *testing.T) {
	p := credPayload{
		values:   map[string][]byte{"b": []byte("two"), "a": []byte("one")},
		manifest: "a env A\nb env B\n",
	}
	got := string(credStream(p))
	want := "byre-credentials 1\n" +
		"item manifest\n" + base64.StdEncoding.EncodeToString([]byte("a env A\nb env B\n")) + "\n" +
		"item a\n" + base64.StdEncoding.EncodeToString([]byte("one")) + "\n" +
		"item b\n" + base64.StdEncoding.EncodeToString([]byte("two")) + "\n" +
		"done\n"
	if got != want {
		t.Fatalf("stream:\n%q\nwant:\n%q", got, want)
	}
}
