package commands

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/project"
)

func TestCredentialsInitAndSetRoundtrip(t *testing.T) {
	p, proj := testPaths(t)
	passphraseSeam(t, "pw", "pw") // init: passphrase + confirm
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	if err := CredentialsInit(s, proj, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(errBuf.String(), "vault created") {
		t.Fatalf("init notice: %s", errBuf.String())
	}
	// Second init refuses (the vault-exists rule); --replace recreates.
	if err := CredentialsInit(s, proj, false); !errors.Is(err, credentials.ErrVaultExists) {
		t.Fatalf("second init: %v, want ErrVaultExists", err)
	}
	// set via masked prompt (TTY path).
	passphraseSeam(t, "sk-live-9")
	if err := CredentialsSet(s, proj, "stripe"); err != nil {
		t.Fatalf("set: %v", err)
	}
	// The undeclared-name hint names the remedy.
	if !strings.Contains(errBuf.String(), "credentials declare stripe") {
		t.Fatalf("undeclared hint: %s", errBuf.String())
	}
	v := credentials.Open(p.Dir, p.ID)
	if got := v.EntryNames(); len(got) != 1 || got[0] != "stripe" {
		t.Fatalf("stored names = %v", got)
	}
	u, err := v.Unlock("pw")
	if err != nil {
		t.Fatal(err)
	}
	val, oc, _ := u.Decrypt("stripe")
	if oc != credentials.OutcomeDelivered || string(val) != "sk-live-9" {
		t.Fatalf("roundtrip: %s %q", oc, val)
	}
}

func TestCredentialsSetPipedStdin(t *testing.T) {
	p, proj := testPaths(t)
	passphraseSeam(t, "pw", "pw")
	var errBuf bytes.Buffer
	if err := CredentialsInit(ttyStreams(&errBuf), proj, false); err != nil {
		t.Fatal(err)
	}
	// Piped (non-TTY): the value is stdin whole, one trailing newline
	// stripped — the `op read ... | byre credentials set` shape.
	s := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("tok-from-pipe\n"), TTY: false}
	if err := CredentialsSet(s, proj, "github"); err != nil {
		t.Fatalf("piped set: %v", err)
	}
	v := credentials.Open(p.Dir, p.ID)
	u, _ := v.Unlock("pw")
	val, oc, _ := u.Decrypt("github")
	if oc != credentials.OutcomeDelivered || string(val) != "tok-from-pipe" {
		t.Fatalf("piped roundtrip: %s %q", oc, val)
	}
}

func TestCredentialsInitRefusalsAndMismatch(t *testing.T) {
	_, proj := testPaths(t)
	var errBuf bytes.Buffer
	// Non-TTY init refuses (the passphrase never rides a pipe).
	s := Streams{Out: io.Discard, Err: &errBuf, In: strings.NewReader("pw\npw\n"), TTY: false}
	if err := CredentialsInit(s, proj, false); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("non-TTY init: %v", err)
	}
	// Mismatched confirm aborts with nothing created.
	passphraseSeam(t, "pw", "other")
	if err := CredentialsInit(ttyStreams(&errBuf), proj, false); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("mismatch: %v", err)
	}
	// Empty passphrase refused by the empty-passphrase rule.
	passphraseSeam(t, "")
	if err := CredentialsInit(ttyStreams(&errBuf), proj, false); err == nil || !strings.Contains(err.Error(), "empty passphrase") {
		t.Fatalf("empty: %v", err)
	}
	p, _ := project.Resolve(proj)
	if credentials.Open(p.Dir, p.ID).Exists() {
		t.Fatal("aborted init must create nothing")
	}
}

func TestCredentialsUnset(t *testing.T) {
	p, proj := testPaths(t)
	passphraseSeam(t, "pw", "pw", "val")
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	if err := CredentialsInit(s, proj, false); err != nil {
		t.Fatal(err)
	}
	if err := CredentialsSet(s, proj, "a"); err != nil {
		t.Fatal(err)
	}
	if err := CredentialsUnset(s, proj, "a"); err != nil {
		t.Fatal(err)
	}
	if got := credentials.Open(p.Dir, p.ID).EntryNames(); len(got) != 0 {
		t.Fatalf("names after unset = %v", got)
	}
}

func TestCredentialsRekey(t *testing.T) {
	p, proj := testPaths(t)
	passphraseSeam(t, "pw", "pw", "v1", "pw", "new", "new")
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	if err := CredentialsInit(s, proj, false); err != nil {
		t.Fatal(err)
	}
	if err := CredentialsSet(s, proj, "a"); err != nil {
		t.Fatal(err)
	}
	if err := CredentialsRekey(s, proj); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	v := credentials.Open(p.Dir, p.ID)
	if _, err := v.Unlock("pw"); !errors.Is(err, credentials.ErrBadPassphrase) {
		t.Fatalf("old passphrase after rekey: %v", err)
	}
	u, err := v.Unlock("new")
	if err != nil {
		t.Fatal(err)
	}
	if val, oc, _ := u.Decrypt("a"); oc != credentials.OutcomeDelivered || string(val) != "v1" {
		t.Fatalf("entries after rekey: %s %q", oc, val)
	}
	// The identity-unchanged caveat is said out loud, with the remedy.
	if !strings.Contains(errBuf.String(), "init --replace") {
		t.Fatalf("rekey caveat: %s", errBuf.String())
	}
}

func TestCredentialsDeclareUndeclareList(t *testing.T) {
	p, proj := testPaths(t)
	var out, errBuf bytes.Buffer
	s := Streams{Out: &out, Err: &errBuf, In: strings.NewReader(""), TTY: true}
	if err := CredentialsDeclare(s, proj, false, "stripe", "env", "STRIPE_KEY"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	// The declaration landed in the project layer via the shared rails.
	raw, err := os.ReadFile(filepath.Join(p.Dir, config.ProjectConfigName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[[credentials]]", `name = "stripe"`, `target = "STRIPE_KEY"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("project config missing %q:\n%s", want, raw)
		}
	}
	// declare validates shape by the credential rules.
	if err := CredentialsDeclare(s, proj, false, "bad name", "env", "X"); err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Fatalf("declare bad name: %v", err)
	}
	if err := CredentialsDeclare(s, proj, false, "a", "env", "BYRE_EGRESS"); err == nil || !strings.Contains(err.Error(), "BYRE_ namespace") {
		t.Fatalf("declare reserved target: %v", err)
	}
	// list shows the declared row unset, then set after a value lands.
	if err := CredentialsList(s, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stripe\tenv → STRIPE_KEY\tunset") {
		t.Fatalf("list: %s", out.String())
	}
	passphraseSeam(t, "pw", "pw", "sk")
	if err := CredentialsInit(s, proj, false); err != nil {
		t.Fatal(err)
	}
	if err := CredentialsSet(s, proj, "stripe"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := CredentialsList(s, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stripe\tenv → STRIPE_KEY\tset") {
		t.Fatalf("list after set: %s", out.String())
	}
	// undeclare removes the declaration; the stored value stays and lists
	// as stored-not-declared.
	if err := CredentialsUndeclare(s, proj, false, "stripe"); err != nil {
		t.Fatalf("undeclare: %v", err)
	}
	out.Reset()
	if err := CredentialsList(s, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stored, not declared") {
		t.Fatalf("list after undeclare: %s", out.String())
	}
}

func TestCredentialsSetRefusesEmptyValue(t *testing.T) {
	_, proj := testPaths(t)
	passphraseSeam(t, "pw", "pw", "")
	var errBuf bytes.Buffer
	s := ttyStreams(&errBuf)
	if err := CredentialsInit(s, proj, false); err != nil {
		t.Fatal(err)
	}
	if err := CredentialsSet(s, proj, "a"); err == nil || !strings.Contains(err.Error(), "empty value") {
		t.Fatalf("empty set: %v", err)
	}
}
