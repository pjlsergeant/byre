package configui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/tomldoc"
)

// fakeCredAdmin stands in for the commands-side write path (whose locking and
// compare-and-swap are tested there, against real files). It does the real
// CRYPTO — mint an identity under the passphrase the modal handed over,
// encrypt to that identity's recipient — so a test can open the row again the
// way a launch does and prove the editor passed the right key, kind, value and
// passphrase through.
//
// And it writes the real FILE: the row, the identity, and the accept's
// removals land in one document edit whose bytes come back as the result, the
// way the write path lands them. That is what makes the editor's own claims
// checkable here — a buffer that says "saved" is checked against what a
// quit-without-^s leaves on disk, not against a promise.
type fakeCredAdmin struct {
	path       string // the model's file; credModel points this at it
	disclosure string
	identity   []byte // nil until the first Set mints one
	recipient  string
	passphrase string // as handed to the mint
	writes     []CredentialWrite
	rows       map[string]string
	sets       int
	mints      int
	err        error // the write path's refusal, when the test wants one
	// concurrent runs INSIDE the write's window: after the bytes this write
	// landed, before the editor takes its baseline. That is exactly where
	// another session's write used to be adopted as this session's baseline,
	// because the baseline was re-read from disk instead of taken from the
	// write.
	concurrent func()
}

func newFakeCredAdmin() *fakeCredAdmin {
	return &fakeCredAdmin{rows: map[string]string{}}
}

func (f *fakeCredAdmin) Disclosure() string            { return f.disclosure }
func (f *fakeCredAdmin) HasIdentity() (bool, error)    { return f.identity != nil, nil }
func (f *fakeCredAdmin) mintedUnder() string           { return f.passphrase }
func (f *fakeCredAdmin) row(key string) (string, bool) { r, ok := f.rows[key]; return r, ok }

// lastWrite is the accept the editor last asked for — the removals included,
// which is how a test asks whether the editor named the whole change.
func (f *fakeCredAdmin) lastWrite() CredentialWrite { return f.writes[len(f.writes)-1] }

func (f *fakeCredAdmin) Set(w CredentialWrite) (CredentialResult, error) {
	f.writes = append(f.writes, w)
	if f.err != nil {
		return CredentialResult{}, f.err
	}
	if err := credentials.ValidateValue(w.Value, w.Kind); err != nil {
		return CredentialResult{}, err
	}
	minted := false
	if f.identity == nil {
		wrapped, recipient, err := credentials.NewIdentity(w.Passphrase)
		if err != nil {
			return CredentialResult{}, err
		}
		f.identity, f.recipient, f.passphrase = wrapped, recipient, w.Passphrase
		f.mints++
		minted = true
	}
	blob, err := credentials.EncryptValue(f.recipient, w.Key, w.Kind, w.Value)
	if err != nil {
		return CredentialResult{}, err
	}
	row, err := config.FormatEncryptedRow(w.Kind, blob)
	if err != nil {
		return CredentialResult{}, err
	}
	after, err := f.applyToFile(w, row, minted)
	if err != nil {
		return CredentialResult{}, err
	}
	f.sets++
	f.rows[w.Key] = row
	delete(f.rows, w.RemoveEnvFromHost)
	return CredentialResult{Row: row, File: after}, nil
}

// applyToFile lands one write: the identity (on a mint), the removals the
// accept carries, and the row — one document, one write, one set of bytes
// handed back as the caller's new baseline.
func (f *fakeCredAdmin) applyToFile(w CredentialWrite, row string, minted bool) ([]byte, error) {
	if f.path == "" {
		return nil, errors.New("fakeCredAdmin has no file to write (credModel sets it)")
	}
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	doc, err := tomldoc.Load(raw)
	if err != nil {
		return nil, err
	}
	if minted {
		id := base64.StdEncoding.EncodeToString(f.identity)
		if err := doc.SetKey([]string{"credentials"}, "identity", strconv.Quote(id)); err != nil {
			return nil, err
		}
		if err := doc.SetKey([]string{"credentials"}, "recipient", strconv.Quote(f.recipient)); err != nil {
			return nil, err
		}
	}
	if w.RemoveEnv != "" {
		if err := doc.RemoveKey([]string{"env"}, w.RemoveEnv); err != nil {
			return nil, err
		}
	}
	if w.RemoveEnvFromHost != "" {
		if err := doc.RemoveKey([]string{config.EnvFromHostTable}, w.RemoveEnvFromHost); err != nil {
			return nil, err
		}
	}
	if err := doc.SetKey([]string{config.EnvFromHostTable}, w.Key, strconv.Quote(row)); err != nil {
		return nil, err
	}
	out := doc.Bytes()
	if err := os.WriteFile(f.path, out, 0o644); err != nil {
		return nil, err
	}
	if f.concurrent != nil {
		f.concurrent()
	}
	return out, nil
}

// open decrypts a row the editor wrote, the way a launch does.
func (f *fakeCredAdmin) open(t *testing.T, key string, passphrase string) []byte {
	t.Helper()
	row, ok := f.rows[key]
	if !ok {
		t.Fatalf("no row was written for %s", key)
	}
	parsed, isCred, err := config.ParseEncryptedRow(key, row)
	if err != nil || !isCred {
		t.Fatalf("row %q is not a credential row: %v", row, err)
	}
	id, err := credentials.UnwrapIdentity(f.identity, passphrase)
	if err != nil {
		t.Fatalf("unwrap under %q: %v", passphrase, err)
	}
	value, outcome, err := id.DecryptValue(parsed.Key, parsed.Kind, parsed.Blob)
	if err != nil {
		t.Fatalf("decrypt %s: %s %v", key, outcome, err)
	}
	return value
}

// credModel is the Env screen with a credential write path attached, on a real
// (empty) file so the write's re-baselining has something to read.
func credModel(t *testing.T, admin CredentialAdmin, local map[string]string) model {
	t.Helper()
	return credModelWith(t, admin, nil, local)
}

// credModelWith is credModel with [env] literals too, for the conversion the
// credential path has to perform ON DISK rather than in the buffer alone.
func credModelWith(t *testing.T, admin CredentialAdmin, env, hostEnv map[string]string) model {
	t.Helper()
	credentials.SetWorkFactorForTesting(10)
	path := filepath.Join(t.TempDir(), "byre.config")
	// The rows go in the FILE, not just the config value: newModel re-parses
	// the bytes it reads at open (one read for state and drift baseline
	// alike), so a fixture that only passed a Config would open empty.
	raw := "# Managed by `byre config`.\n"
	raw += credTable("env", env) + credTable(config.EnvFromHostTable, hostEnv)
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	// The fake writes that same file, so the editor's baseline and a
	// quit-without-^s are judged against real bytes.
	if f, ok := admin.(*fakeCredAdmin); ok {
		f.path = path
	}
	m := newModel("t", path, config.Config{Env: env, EnvFromHost: hostEnv}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.creds = admin
	m.listField = fEnv
	return m
}

func credTable(name string, rows map[string]string) string {
	if len(rows) == 0 {
		return ""
	}
	out := "\n[" + name + "]\n"
	for _, k := range slices.Sorted(maps.Keys(rows)) {
		out += k + " = " + strconv.Quote(rows[k]) + "\n"
	}
	return out
}

// addCredential opens the add editor on the credential scheme, with the kind
// picker set and the key and value typed in, exactly as the keystrokes leave
// it.
func addCredential(m model, kindSel int, key, value string) model {
	m.itemHostEnv = false
	m = m.startItem(-1)
	m.itemMode = schemeCredential
	m.itemMode2 = kindSel
	m = m.syncHostEnvLabel()
	m.inputs[0].SetValue(key)
	m.inputs[1].SetValue(value)
	return m
}

// The picker is where a credential is REACHABLE from at all: without the two
// kinds on it, the editor can show credential rows and never author one, and
// "expert vocabulary, hand-edit it" is not an answer byre gives (P0).
func TestEnvPickerOffersTheCredentialKinds(t *testing.T) {
	m := credModel(t, newFakeCredAdmin(), nil)
	m.itemHostEnv = false
	m = m.startItem(-1)
	if got := m.itemModeOpts[schemeCredential]; !strings.Contains(got, "credential") {
		t.Fatalf("the Source picker offers %v — no credential option", m.itemModeOpts)
	}
	// The kind is the SECOND picker, and it appears for exactly that scheme:
	// what the box gets (a variable, or a file) is its own closed question.
	m.itemMode = schemeCredential
	m = m.syncHostEnvLabel()
	if !m.itemHasMode2 || strings.Join(m.itemMode2Opts, " ") != "env var file" {
		t.Fatalf("kind picker: has=%v opts=%v", m.itemHasMode2, m.itemMode2Opts)
	}
	m.itemMode = schemeValue
	m = m.syncHostEnvLabel()
	if m.itemHasMode2 {
		t.Fatal("the kind picker stayed on a scheme that has no kind")
	}
	// And where nothing can write one, the option is absent rather than
	// present-and-refusing: an editor whose file no credentials verb targets
	// (--global) must not offer a door that only ever closes.
	none := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
	none.listField = fEnv
	none = none.startItem(-1)
	if strings.Contains(strings.Join(none.itemModeOpts, " "), "credential") {
		t.Fatalf("an editor with no write path offers %v", none.itemModeOpts)
	}
}

// The value is typed hidden and stays hidden: not in the form, not in the row,
// not in the status line the write leaves behind.
func TestCredentialValueIsNeverRendered(t *testing.T) {
	const secret = "sk-live-verysecret"
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	m := credModel(t, admin, nil)
	m = addCredential(m, credKindEnv, "STRIPE_KEY", secret)
	if v := m.viewItem(); strings.Contains(v, secret) {
		t.Fatalf("the form rendered the value:\n%s", v)
	} else if !strings.Contains(v, "•") {
		t.Fatalf("the value box is not masked:\n%s", v)
	}
	done := m.commitItem()
	if done.itemErr != "" {
		t.Fatalf("the write was refused: %s", done.itemErr)
	}
	for name, s := range map[string]string{
		"status": done.status,
		"list":   done.viewList(),
		"row":    hostEnvLine("STRIPE_KEY", done.hostEnv[0].Value),
	} {
		if strings.Contains(s, secret) {
			t.Fatalf("the %s carries the plaintext: %q", name, s)
		}
	}
	// The row that landed is the ciphertext.
	if !config.IsCredentialSource(done.hostEnv[0].Value) {
		t.Fatalf("the row is %q, want a credential row", done.hostEnv[0].Value)
	}
}

// mintFor gives a fake admin a file that ALREADY has an identity.
func mintFor(t *testing.T, passphrase string) ([]byte, string) {
	t.Helper()
	credentials.SetWorkFactorForTesting(10)
	wrapped, recipient, err := credentials.NewIdentity(passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return wrapped, recipient
}

// A file's FIRST credential mints its identity, so the modal asks for the
// passphrase that will wrap it — twice — and only then does anything get
// written. The row it writes is a real one: it decrypts under that passphrase.
func TestFirstCredentialAsksForThePassphraseAndWritesADecryptableRow(t *testing.T) {
	admin := newFakeCredAdmin()
	m := credModel(t, admin, nil)
	m = addCredential(m, credKindEnv, "STRIPE_KEY", "sk-live-1")

	m = m.commitItem()
	if m.mode != modeCredPass {
		t.Fatalf("mode = %v, want the passphrase modal before a file's first credential", m.mode)
	}
	if admin.sets != 0 {
		t.Fatal("something was written before the passphrase was chosen")
	}
	if v := m.viewCredPass(); !strings.Contains(v, "passphrase") || !strings.Contains(strings.ToLower(v), "no recovery") {
		t.Fatalf("the modal must say what the passphrase protects and what forgetting it costs:\n%s", v)
	}

	// An empty passphrase is worthless, and the modal says so in the shared
	// words rather than accepting it.
	m = typeCredPass(t, m, "", "")
	if m.credPassErr != credentials.EmptyPassphraseWorthless {
		t.Fatalf("credPassErr = %q, want the shared refusal", m.credPassErr)
	}
	// A mismatch is caught before anything is written, too.
	m = typeCredPass(t, m, "hunter2", "hunter3")
	if m.credPassErr == "" || admin.sets != 0 {
		t.Fatalf("a mismatched confirmation wrote something (err=%q sets=%d)", m.credPassErr, admin.sets)
	}

	m = typeCredPass(t, m, "hunter2", "hunter2")
	if m.mode != modeList {
		t.Fatalf("mode = %v after the passphrase was confirmed, want back at the list", m.mode)
	}
	if admin.mints != 1 || admin.sets != 1 {
		t.Fatalf("mints=%d sets=%d, want exactly one of each", admin.mints, admin.sets)
	}
	if admin.mintedUnder() != "hunter2" {
		t.Fatalf("the identity was minted under %q", admin.mintedUnder())
	}
	if got := admin.open(t, "STRIPE_KEY", "hunter2"); string(got) != "sk-live-1" {
		t.Fatalf("round trip: %q", got)
	}
	if row, _ := admin.row("STRIPE_KEY"); m.hostEnv[0].Value != row {
		t.Fatalf("working state carries %q, the file has %q", m.hostEnv[0].Value, row)
	}
}

// typeCredPass fills the modal's two boxes and confirms, driving it through
// Update the way the keys do.
func typeCredPass(t *testing.T, m model, pw, confirm string) model {
	t.Helper()
	if m.mode != modeCredPass {
		t.Fatalf("mode = %v, want the passphrase modal", m.mode)
	}
	m.credPassInputs[0].SetValue(pw)
	m.credPassInputs[1].SetValue(confirm)
	m.credPassFocus = 1
	next, _ := m.updateCredPass(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(model)
}

// The SECOND credential in a file encrypts to the recipient the block already
// carries: no modal, no second passphrase, no new identity. (That is the whole
// point of wrapping only the identity — setting a value never prompts.)
func TestSecondCredentialReusesTheFilesIdentity(t *testing.T) {
	admin := newFakeCredAdmin()
	m := credModel(t, admin, nil)
	m = typeCredPass(t, addCredential(m, credKindEnv, "FIRST", "one").commitItem(), "pw", "pw")

	m = addCredential(m, credKindFile, "SECOND", "two").commitItem()
	if m.mode == modeCredPass {
		t.Fatal("the second credential asked for a passphrase again")
	}
	if m.itemErr != "" {
		t.Fatalf("the second write was refused: %s", m.itemErr)
	}
	if admin.mints != 1 || admin.sets != 2 {
		t.Fatalf("mints=%d sets=%d, want one identity and two values", admin.mints, admin.sets)
	}
	if got := admin.open(t, "SECOND", "pw"); string(got) != "two" {
		t.Fatalf("round trip: %q", got)
	}
	// The kind rides the scheme, so the file row states what the box gets.
	if row, _ := admin.row("SECOND"); !strings.HasPrefix(row, config.EncryptedFileScheme) {
		t.Fatalf("the file-kind row is %q", row)
	}
}

// Opening an existing credential row shows its KIND and an empty value box.
// Leaving the box empty keeps the stored value: it is not readable from here,
// so demanding it be retyped would be asking for a secret the editor could
// never have shown.
func TestEditingACredentialRowKeepsTheValueWhenTheBoxIsLeftEmpty(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	row := encryptedRow(t, admin.recipient, "STRIPE_KEY", credentials.KindEnv, "sk-live-1")
	m := credModel(t, admin, map[string]string{"STRIPE_KEY": row})

	m = openHostEnvRow(t, m, "STRIPE_KEY")
	if m.mode != modeItem {
		t.Fatalf("a well-formed credential row must open the form; status: %q", m.status)
	}
	if m.itemMode != schemeCredential || m.itemMode2 != credKindEnv {
		t.Fatalf("the row opened on scheme %d kind %d, want its own", m.itemMode, m.itemMode2)
	}
	if v := m.inputs[1].Value(); v != "" {
		t.Fatalf("the value box opened holding %q; the stored value is never shown", v)
	}
	if v := m.viewItem(); !strings.Contains(v, "empty keeps it") {
		t.Fatalf("the form must say what an empty box means:\n%s", v)
	}

	done := m.commitItem()
	if done.itemErr != "" {
		t.Fatalf("accepting an unchanged credential was refused: %s", done.itemErr)
	}
	if admin.sets != 0 {
		t.Fatal("an unchanged credential was re-encrypted and rewritten")
	}
	if got := done.assemble().EnvFromHost["STRIPE_KEY"]; got != row {
		t.Fatalf("the ciphertext must round-trip untouched: %q", got)
	}
}

// Re-entering a value REPLACES the stored one, under the same key.
func TestReenteringACredentialValueReplacesIt(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	row := encryptedRow(t, admin.recipient, "STRIPE_KEY", credentials.KindEnv, "sk-live-1")
	m := credModel(t, admin, map[string]string{"STRIPE_KEY": row})
	m = openHostEnvRow(t, m, "STRIPE_KEY")
	m.inputs[1].SetValue("sk-live-2")

	done := m.commitItem()
	if done.itemErr != "" {
		t.Fatalf("replacing a value was refused: %s", done.itemErr)
	}
	if done.mode == modeCredPass {
		t.Fatal("replacing a value in a file that has an identity asked for a passphrase")
	}
	if got := admin.open(t, "STRIPE_KEY", "pw"); string(got) != "sk-live-2" {
		t.Fatalf("round trip: %q", got)
	}
	if got := done.assemble().EnvFromHost["STRIPE_KEY"]; got == row {
		t.Fatal("the working state still carries the old ciphertext")
	}
}

// The two edits a credential row cannot take without a value: its key (the
// payload is stamped with it) and its kind (stamped too). Both refuse by
// NAMING the rule, and neither writes.
func TestCredentialRowRefusesARenameAndAKindChangeWithoutAValue(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	row := encryptedRow(t, admin.recipient, "STRIPE_KEY", credentials.KindEnv, "sk-live-1")

	rename := openHostEnvRow(t, credModel(t, admin, map[string]string{"STRIPE_KEY": row}), "STRIPE_KEY")
	rename.inputs[0].SetValue("OTHER_KEY")
	if got := rename.commitItem(); !strings.Contains(got.itemErr, "bound to its key") {
		t.Fatalf("itemErr = %q, want the key-binding rule", got.itemErr)
	}

	kind := openHostEnvRow(t, credModel(t, admin, map[string]string{"STRIPE_KEY": row}), "STRIPE_KEY")
	kind.itemMode2 = credKindFile
	if got := kind.commitItem(); !strings.Contains(got.itemErr, "re-encrypts") {
		t.Fatalf("itemErr = %q, want the kind-change rule", got.itemErr)
	}
	if admin.sets != 0 {
		t.Fatal("a refused edit still wrote")
	}
}

// Switching a credential row to another source is an UNSET of that row: the
// ciphertext leaves with it, and byre keeps no copy. The editor says so
// instead of quietly dropping a value nothing can bring back.
func TestSwitchingAwayFromACredentialSaysTheCiphertextGoes(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	row := encryptedRow(t, admin.recipient, "STRIPE_KEY", credentials.KindEnv, "sk-live-1")
	m := credModel(t, admin, map[string]string{"STRIPE_KEY": row})
	m = openHostEnvRow(t, m, "STRIPE_KEY")
	m.itemMode = schemeEnv
	m = m.syncHostEnvLabel()
	if v := m.viewItem(); !strings.Contains(v, "ciphertext goes with the row") {
		t.Fatalf("the form must say what leaving the credential kind costs:\n%s", v)
	}
	m.inputs[1].SetValue("STRIPE_KEY")

	done := m.commitItem()
	if done.itemErr != "" {
		t.Fatalf("switching source was refused: %s", done.itemErr)
	}
	if got := done.assemble().EnvFromHost["STRIPE_KEY"]; got != "env:STRIPE_KEY" {
		t.Fatalf("EnvFromHost[STRIPE_KEY] = %q, want the new source", got)
	}
	if !strings.Contains(done.status, "ciphertext") {
		t.Fatalf("status = %q, want the loss stated", done.status)
	}
	if admin.sets != 0 {
		t.Fatal("leaving the credential kind wrote through the credential path")
	}
}

// The write path's refusals are the editor's refusals: an oversize env value
// comes back as the rule and the size, at the form, with the value nowhere in
// it. (The caps themselves are credentials.ValidateValue's — this pins that
// the editor surfaces them legibly rather than restating them.)
func TestCredentialCapRefusalIsSurfacedAtTheForm(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	m := credModel(t, admin, nil)
	big := strings.Repeat("A", credentials.MaxEnvValue+1)
	m = addCredential(m, credKindEnv, "BIG", big)

	done := m.commitItem()
	if done.itemErr == "" {
		t.Fatal("an oversize env value was accepted")
	}
	if !strings.Contains(done.itemErr, "cap") || !strings.Contains(done.itemErr, "65537") {
		t.Fatalf("itemErr = %q, want the rule and the offending size", done.itemErr)
	}
	if strings.Contains(done.itemErr, big) {
		t.Fatal("the refusal echoed the value")
	}
	if done.mode != modeItem {
		t.Fatalf("mode = %v, want the form still open with its error", done.mode)
	}
	// The caps are the kind's, and the form states them where the value is
	// typed rather than only in the refusal.
	if v := done.viewItem(); !strings.Contains(v, "64 KiB") {
		t.Fatalf("the form does not name the env-kind cap:\n%s", v)
	}
	if fileNote := credentialKindNote(credKindFile); !strings.Contains(fileNote, "256 KiB") {
		t.Fatalf("the file-kind note = %q, want the 256 KiB ceiling", fileNote)
	}
}

// A layer file reaches every project extending it, so the editor states that
// BEFORE the value is accepted — in the form and again in the modal, which is
// the last screen before a first write.
func TestLayerWriteTargetIsDisclosedBeforeTheValueIsAccepted(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.disclosure = "writes to layer acme (/x/layer.config), used by 3 projects — this changes the value for every project extending it"
	m := credModel(t, admin, nil)
	m = addCredential(m, credKindEnv, "STRIPE_KEY", "sk-live-1")
	if v := m.viewItem(); !strings.Contains(v, "layer acme") || !strings.Contains(v, "3 projects") {
		t.Fatalf("the form does not disclose the write target:\n%s", v)
	}
	m = m.commitItem()
	if v := m.viewCredPass(); !strings.Contains(v, "layer acme") {
		t.Fatalf("the passphrase modal does not disclose the write target:\n%s", v)
	}
	if admin.sets != 0 {
		t.Fatal("the value was written before the disclosure could stop it")
	}
}

// esc out of the modal writes NOTHING — no identity, no row — and lands back
// in the form with the value still in its (masked) box.
func TestEscapingThePassphraseModalWritesNothing(t *testing.T) {
	admin := newFakeCredAdmin()
	m := credModel(t, admin, nil)
	m = addCredential(m, credKindEnv, "STRIPE_KEY", "sk-live-1").commitItem()
	back, _ := m.updateCredPass(tea.KeyMsg{Type: tea.KeyEsc})
	got := back.(model)
	if got.mode != modeItem {
		t.Fatalf("mode = %v, want the form back", got.mode)
	}
	if admin.sets != 0 || admin.mints != 0 {
		t.Fatalf("esc wrote: sets=%d mints=%d", admin.sets, admin.mints)
	}
	if got.credPending != nil {
		t.Fatal("the pending value survived the cancel")
	}
	if len(got.hostEnv) != 0 {
		t.Fatalf("hostEnv = %+v, want nothing added", got.hostEnv)
	}
}

// A refusal on the way out of the modal is the other exit from it, and it must
// leave the same nothing behind: the write path said no (an identity appeared
// under the form, a value over its cap), so the plaintext waiting on the
// passphrase has nothing left to wait for. It is held HERE and nowhere else, so
// only this path can drop it.
func TestARefusedWriteDropsThePendingValue(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.err = errors.New("byre.config gained a credentials identity while the form was open")
	m := credModel(t, admin, nil)
	m = addCredential(m, credKindEnv, "STRIPE_KEY", "sk-live-1").commitItem()
	got := typeCredPass(t, m, "new-pw", "new-pw")
	if got.mode != modeItem {
		t.Fatalf("mode = %v, want the form back", got.mode)
	}
	if !strings.Contains(got.itemErr, "gained a credentials identity") {
		t.Fatalf("itemErr = %q, want the write path's refusal", got.itemErr)
	}
	if got.credPending != nil {
		t.Fatal("the pending value survived the refusal")
	}
}

// The write lands on disk on accept, so a buffer that was CLEAN stays clean:
// quitting after setting a credential must not claim unsaved changes, and the
// next ^s must not see the editor's own write as another session's drift.
func TestACleanBufferStaysCleanAfterACredentialWrite(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	m := credModel(t, admin, nil)
	if m.dirty() {
		t.Fatal("the fixture opened dirty")
	}
	done := addCredential(m, credKindEnv, "STRIPE_KEY", "sk-live-1").commitItem()
	if done.itemErr != "" {
		t.Fatalf("the write was refused: %s", done.itemErr)
	}
	if done.dirty() {
		t.Fatal("a credential write left the buffer looking unsaved, though it is on disk")
	}
	// And the row is in the assembled config, so a later save writes the same
	// value back instead of reconciling it away.
	if got := done.assemble().EnvFromHost["STRIPE_KEY"]; !config.IsCredentialSource(got) {
		t.Fatalf("EnvFromHost[STRIPE_KEY] = %q", got)
	}
}

// openEnvRow opens an [env] literal's row in the item editor.
func openEnvRow(t *testing.T, m model, key string) model {
	t.Helper()
	m.listField = fEnv
	for i, kv := range m.env {
		if kv.Key == key {
			m.itemHostEnv = false
			return m.startItem(i)
		}
	}
	t.Fatalf("no [env] row for %q", key)
	return m
}

// fileState is the config file as it stands, parsed the way a launch reads it.
func fileState(t *testing.T, path string) (config.Config, []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatalf("the editor's write left a file byre cannot read: %v\n%s", err, raw)
	}
	return cfg, raw
}

// Converting an [env] literal to a credential is ONE change, and the accept
// writes all of it. The literal leaving is not a buffer edit awaiting ^s: an
// [env] literal takes its key out of env_from_host entirely (ADR 0026), so a
// quit before ^s would leave the box taking the old plaintext while this screen
// said the credential was set.
func TestConvertingAnEnvLiteralRemovesItOnDiskWithTheWrite(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	m := credModelWith(t, admin, map[string]string{"STRIPE_KEY": "plaintext-key", "KEEP": "other"}, nil)
	if m.dirty() {
		t.Fatal("the fixture opened dirty")
	}

	m = openEnvRow(t, m, "STRIPE_KEY")
	m.itemMode = schemeCredential
	m.itemMode2 = credKindEnv
	m = m.syncHostEnvLabel()
	m.inputs[1].SetValue("sk-live-1")
	done := m.commitItem()
	if done.itemErr != "" {
		t.Fatalf("the conversion was refused: %s", done.itemErr)
	}

	// The write NAMED the removal, so the file's own mutation carried it.
	if got := admin.lastWrite().RemoveEnv; got != "STRIPE_KEY" {
		t.Fatalf("the write asked to remove %q — the accept's literal was left for ^s", got)
	}
	// Quit here, without ^s: the file holds the encrypted row and nothing else
	// of that key.
	cfg, raw := fileState(t, admin.path)
	if v, still := cfg.Env["STRIPE_KEY"]; still {
		t.Fatalf("[env] still holds %q after the accept:\n%s", v, raw)
	}
	if cfg.Env["KEEP"] != "other" {
		t.Fatalf("the accept took an [env] row it was not given: %q", cfg.Env["KEEP"])
	}
	if !config.IsCredentialSource(cfg.EnvFromHost["STRIPE_KEY"]) {
		t.Fatalf("env_from_host[STRIPE_KEY] = %q", cfg.EnvFromHost["STRIPE_KEY"])
	}
	// And the launch side delivers it: nothing shadows the row.
	groups, gerr := config.EncryptedRows([]config.CascadeFile{{Label: "project", Path: admin.path, Raw: raw, Cfg: cfg}})
	if gerr != nil {
		t.Fatal(gerr)
	}
	if len(groups) != 1 || len(groups[0].Rows) != 1 || groups[0].Rows[0].Key != "STRIPE_KEY" {
		t.Fatalf("the converted row is not delivered: %+v", groups)
	}
	// The buffer mirrors that file, so a clean buffer stays clean truthfully.
	if done.dirty() {
		t.Fatal("the conversion left the buffer unsaved though the whole change is on disk")
	}
	if _, still := done.assemble().Env["STRIPE_KEY"]; still {
		t.Fatal("the working state kept the literal it converted")
	}
}

// The sibling: an ordinary passthrough re-authored as a credential under a NEW
// key. The buffer replaces the row; the file must too, or the old row survives
// a "clean" quit and keeps delivering the host value.
func TestReauthoringARowUnderANewKeyRemovesTheOldRowOnDisk(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	m := credModel(t, admin, map[string]string{"OLD_KEY": "env:HOST_TOKEN", "KEEP": "env:KEEP"})

	m = openHostEnvRow(t, m, "OLD_KEY")
	m.inputs[0].SetValue("NEW_KEY")
	m.itemMode = schemeCredential
	m.itemMode2 = credKindEnv
	m = m.syncHostEnvLabel()
	m.inputs[1].SetValue("sk-live-1")
	done := m.commitItem()
	if done.itemErr != "" {
		t.Fatalf("the re-authoring was refused: %s", done.itemErr)
	}

	if got := admin.lastWrite().RemoveEnvFromHost; got != "OLD_KEY" {
		t.Fatalf("the write asked to remove %q — the replaced row was left for ^s", got)
	}
	cfg, raw := fileState(t, admin.path)
	if v, still := cfg.EnvFromHost["OLD_KEY"]; still {
		t.Fatalf("env_from_host still holds OLD_KEY = %q:\n%s", v, raw)
	}
	if cfg.EnvFromHost["KEEP"] != "env:KEEP" {
		t.Fatalf("the accept took a row it was not given: %q", cfg.EnvFromHost["KEEP"])
	}
	if !config.IsCredentialSource(cfg.EnvFromHost["NEW_KEY"]) {
		t.Fatalf("env_from_host[NEW_KEY] = %q", cfg.EnvFromHost["NEW_KEY"])
	}
	if done.dirty() {
		t.Fatal("the re-authoring left the buffer unsaved though the whole change is on disk")
	}
}

// A buffer that was ALREADY dirty stays dirty: its other edits are still
// unsaved, and the credential write says nothing about them. What it must not
// do is take those edits to disk on the credential's behalf.
func TestADirtyBufferStaysDirtyAfterACredentialWrite(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	m := credModelWith(t, admin, map[string]string{"KEEP": "other"}, nil)
	m.apt = append(m.apt, "ripgrep") // an unrelated, unsaved edit
	if !m.dirty() {
		t.Fatal("the fixture did not take the unsaved edit")
	}

	done := addCredential(m, credKindEnv, "STRIPE_KEY", "sk-live-1").commitItem()
	if done.itemErr != "" {
		t.Fatalf("the write was refused: %s", done.itemErr)
	}
	if !done.dirty() {
		t.Fatal("the credential write claimed the buffer's OTHER edits were saved")
	}
	cfg, _ := fileState(t, admin.path)
	if len(cfg.Apt) != 0 {
		t.Fatalf("the credential write carried an unrelated edit to disk: %v", cfg.Apt)
	}
	// The credential itself IS on disk, and a ^s over it is not drift.
	if !config.IsCredentialSource(cfg.EnvFromHost["STRIPE_KEY"]) {
		t.Fatalf("env_from_host[STRIPE_KEY] = %q", cfg.EnvFromHost["STRIPE_KEY"])
	}
	saved := done.save()
	if saved.confirmOverwrite || saved.errMsg != "" {
		t.Fatalf("^s over the editor's own credential write prompted: overwrite=%v err=%q", saved.confirmOverwrite, saved.errMsg)
	}
	if cfg, _ := fileState(t, admin.path); len(cfg.Apt) != 1 {
		t.Fatalf("the save did not land the other edits: %v", cfg.Apt)
	}
}

// The baseline a credential write leaves is what THAT write put on disk, taken
// under its lock. A concurrent writer landing afterwards is another session's
// change: the next ^s must see it as drift and ask, not reconcile over it
// silently (which is what a baseline re-read after the lock produced).
func TestAConcurrentWriteAfterACredentialWriteIsStillDrift(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	m := credModel(t, admin, nil)
	// Another session's write, landing in the window between this write and the
	// baseline the editor keeps.
	admin.concurrent = func() {
		raw, err := os.ReadFile(admin.path)
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(admin.path, append(raw, []byte("\n[env]\nFROM_OTHER = \"1\"\n")...), 0o644); err != nil {
			t.Error(err)
		}
	}
	done := addCredential(m, credKindEnv, "STRIPE_KEY", "sk-live-1").commitItem()
	if done.itemErr != "" {
		t.Fatalf("the write was refused: %s", done.itemErr)
	}
	_, after := fileState(t, admin.path)
	if bytes.Equal(done.saveBase, after) {
		t.Fatal("the editor took the other session's bytes as its own baseline")
	}
	if done.saveBaseErr != nil {
		t.Fatalf("baseline error: %v", done.saveBaseErr)
	}
	saved := done.save()
	if !saved.confirmOverwrite {
		t.Fatalf("^s did not see the foreign write as drift (err=%q status=%q)", saved.errMsg, saved.status)
	}
	if cfg, _ := fileState(t, admin.path); cfg.Env["FROM_OTHER"] != "1" {
		t.Fatal("the save overwrote the other session's change instead of asking")
	}
}

// A file with credential rows and NO identity block: the modal is reached (a
// value is being set), and it must not announce a file that "holds no
// credentials yet" over a screen listing them. It proceeds — the rows are
// already undecryptable and minting worsens nothing — and says what the new
// passphrase does not do.
func TestThePassphraseModalTellsTheTruthOverOrphanedRows(t *testing.T) {
	admin := newFakeCredAdmin() // no identity: the file's block is gone
	orphan := encryptedRow(t, mintRecipient(t, "gone"), "OLD_KEY", credentials.KindEnv, "unreachable")
	m := credModel(t, admin, map[string]string{"OLD_KEY": orphan})
	m.probeCredentialIdentity()

	form := addCredential(m, credKindEnv, "STRIPE_KEY", "sk-live-1")
	if v := form.viewItem(); !strings.Contains(v, "credential rows have no identity") {
		t.Fatalf("the form claims the file has no credentials:\n%s", v)
	}
	modal := form.commitItem()
	if modal.mode != modeCredPass {
		t.Fatalf("mode = %v, want the passphrase modal", modal.mode)
	}
	v := modal.viewCredPass()
	if strings.Contains(v, "holds no credentials yet") {
		t.Fatalf("the modal denies the rows the screen behind it lists:\n%s", v)
	}
	if !strings.Contains(v, "1 credential row whose identity is missing") || !strings.Contains(v, "does NOT open it") {
		t.Fatalf("the modal does not say what the new passphrase will not open:\n%s", v)
	}
	// And it proceeds: the value is written, the orphan is left exactly where
	// it was for `byre credentials unset` to clear.
	done := typeCredPass(t, modal, "new-pw", "new-pw")
	if done.itemErr != "" || admin.sets != 1 {
		t.Fatalf("minting over orphaned rows was refused: %q sets=%d", done.itemErr, admin.sets)
	}
	cfg, _ := fileState(t, admin.path)
	if cfg.EnvFromHost["OLD_KEY"] != orphan {
		t.Fatalf("the orphaned row was touched: %q", cfg.EnvFromHost["OLD_KEY"])
	}
}

// mintRecipient is an identity nothing in the test keeps: a row encrypted to it
// is an orphan by construction.
func mintRecipient(t *testing.T, passphrase string) string {
	t.Helper()
	_, recipient := mintFor(t, passphrase)
	return recipient
}

// Crossing the credential boundary CLEARS the value box: a literal typed in
// the open must not become an encrypted value through a picker move, and a
// value typed hidden must not appear the moment the picker leaves.
func TestCrossingTheCredentialBoundaryClearsTheValueBox(t *testing.T) {
	m := credModel(t, newFakeCredAdmin(), nil)
	m.itemHostEnv = false
	m = m.startItem(-1) // opens on `value`
	m.inputs[1].SetValue("plaintext-literal")
	m.itemMode = schemeCredential
	m = m.syncHostEnvLabel()
	if got := m.inputs[1].Value(); got != "" {
		t.Fatalf("the literal survived into the credential box: %q", got)
	}
	m.inputs[1].SetValue("sk-live-1")
	m.itemMode = schemeValue
	m = m.syncHostEnvLabel()
	if got := m.inputs[1].Value(); got != "" {
		t.Fatalf("a hidden value became a visible literal: %q", got)
	}
}

// encryptedRow is a stored credential row for a fixture.
func encryptedRow(t *testing.T, recipient, key string, kind credentials.Kind, value string) string {
	t.Helper()
	blob, err := credentials.EncryptValue(recipient, key, kind, []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	row, err := config.FormatEncryptedRow(kind, blob)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// A credential a LOWER layer sets is overridden here the way every other
// inherited passthrough is: the override door opens the form on that row's
// kind with an empty value box, and the value typed there is written to THIS
// file — never to the layer the row came from.
func TestOverridingAnInheritedCredentialWritesToThisFile(t *testing.T) {
	lower := newFakeCredAdmin()
	lower.identity, lower.recipient = mintFor(t, "layer-pw")
	// A FILE credential on purpose: the kind has to travel with the row, or
	// one keystroke re-sets an inherited file credential as an env var.
	inheritedRow := encryptedRow(t, lower.recipient, "STRIPE_KEY", credentials.KindFile, "layer-value")

	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	m := credModel(t, admin, nil)
	m.inh = Inherited{HasLower: true, Default: config.Config{EnvFromHost: map[string]string{"STRIPE_KEY": inheritedRow}}}

	m = openHostEnvRow(t, m, "STRIPE_KEY") // an inherited row opens the override door
	if m.mode != modeItem {
		t.Fatalf("the override door did not open the form; status: %q", m.status)
	}
	if m.itemMode != schemeCredential || m.itemMode2 != credKindFile {
		t.Fatalf("the override opened on scheme %d kind %d, want the inherited row's", m.itemMode, m.itemMode2)
	}
	if v := m.inputs[1].Value(); v != "" {
		t.Fatalf("the override prefilled the value box with %q — a ciphertext is not a value to re-encode", v)
	}
	m.inputs[1].SetValue("project-value")

	done := m.commitItem()
	if done.itemErr != "" {
		t.Fatalf("the override was refused: %s", done.itemErr)
	}
	if got := admin.open(t, "STRIPE_KEY", "pw"); string(got) != "project-value" {
		t.Fatalf("round trip: %q", got)
	}
	if row, _ := admin.row("STRIPE_KEY"); !strings.HasPrefix(row, config.EncryptedFileScheme) {
		t.Fatalf("the override wrote %q — the inherited row's kind did not travel", config.RenderSource(row))
	}
	if lower.sets != 0 {
		t.Fatal("the override wrote through the lower layer's path")
	}
	if got := done.assemble().EnvFromHost["STRIPE_KEY"]; got == inheritedRow {
		t.Fatal("this file pinned the layer's own ciphertext instead of its new value")
	}
}

// A stored credential row decodes to its KIND and never to its payload: the
// picker has to open on what the row is, and the value box must not be
// prefilled with a ciphertext no form can re-encode.
func TestCredentialSchemesDecodeToTheirKind(t *testing.T) {
	for _, tc := range []struct {
		src  string
		kind int
	}{
		{config.EncryptedScheme + "AAAA", credKindEnv},
		{config.EncryptedFileScheme + "AAAA", credKindFile},
	} {
		got, arg := hostEnvScheme(tc.src)
		if got != schemeCredential {
			t.Errorf("hostEnvScheme(%q) = %d, want the credential scheme", tc.src, got)
		}
		if arg != "" {
			t.Errorf("hostEnvScheme(%q) handed back %q — the payload is not an argument", tc.src, arg)
		}
		if k := credKindSel(tc.src); k != tc.kind {
			t.Errorf("credKindSel(%q) = %d, want %d", tc.src, k, tc.kind)
		}
	}
}

// The Source picker paints every option on ONE line, and the view clips a line
// that runs past the terminal — which would eat the very option a reader had
// selected. That is why the credential KIND is a second picker rather than two
// more spelled-out options here: 80 columns is a real terminal, and an option
// nobody can see is not an option. Measured in display cells, on the form as
// it renders with a credential selected.
func TestTheCredentialFormFitsAnEightyColumnTerminal(t *testing.T) {
	admin := newFakeCredAdmin()
	// The one note byre does not choose the width of: a layer's disclosure
	// carries that layer's path.
	admin.disclosure = "writes to layer acme (/home/someone/.byre/layers/acme/layer.config), used by 3 projects — this changes the value for every project extending it"
	m := credModel(t, admin, nil)
	m.width = 80
	m = addCredential(m, credKindFile, "STRIPE_KEY", "sk-live-1")
	for _, line := range strings.Split(m.viewItem(), "\n") {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("a form line is %d cells wide and will be clipped: %q", w, line)
		}
	}
}

// Deleting a credential row deletes the only copy of that value, so the row
// action says so rather than reading like any other un-pin.
func TestDeletingACredentialRowSaysTheValueGoes(t *testing.T) {
	admin := newFakeCredAdmin()
	admin.identity, admin.recipient = mintFor(t, "pw")
	row := encryptedRow(t, admin.recipient, "STRIPE_KEY", credentials.KindEnv, "sk-live-1")
	m := credModel(t, admin, map[string]string{"STRIPE_KEY": row})
	var target listRow
	for _, r := range m.fieldRows(fEnv) {
		if r.kind == rowHostEnv && r.ident == "STRIPE_KEY" {
			target = r
		}
	}
	m.itemHostEnv = true
	next, _ := m.applyRowAct(actDelete, target)
	got := next.(model)
	if _, still := got.assemble().EnvFromHost["STRIPE_KEY"]; still {
		t.Fatal("the row survived the delete")
	}
	if !strings.Contains(got.status, "ciphertext") {
		t.Fatalf("status = %q, want the loss stated", got.status)
	}
}
