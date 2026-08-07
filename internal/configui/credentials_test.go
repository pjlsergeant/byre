package configui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
)

// keyMsg builds the two key events the modal tests drive.
func keyMsg(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

func init() {
	// Production's pinned scrypt cost is deliberate at a prompt and pure
	// drag in a suite (the unwrap path is identical at any logN).
	credentials.SetWorkFactorForTesting(10)
}

// credTestModel builds a project-target model over a real store dir shaped
// like ~/.byre/projects/<id>/ (the vault handle derives from the file path).
func credTestModel(t *testing.T, cfg config.Config) (model, string) {
	t.Helper()
	store := filepath.Join(t.TempDir(), "projects", "test-project-id")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	m := newModel("t", filepath.Join(store, "byre.config"), cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	return m, store
}

// commitCredential drives the item editor: open add, set kind/name/target
// and (when given) the masked value, commit.
func commitCredential(m model, kind int, name, target, value string) model {
	m.listField = fCredentials
	m = m.startItem(-1)
	m.itemMode = kind
	m.inputs[0].SetValue(name)
	m.inputs[1].SetValue(target)
	if value != "" {
		m.inputs[2].SetValue(value)
	}
	return m.commitItem()
}

func TestCredentialItemEditorStagesMaskedValue(t *testing.T) {
	m, _ := credTestModel(t, config.Config{})
	// The project editor's add form carries the masked Value input.
	m.listField = fCredentials
	probe := m.startItem(-1)
	if len(probe.inputs) != 3 {
		t.Fatalf("project add form inputs = %d, want 3 (name, target, value)", len(probe.inputs))
	}
	if probe.inputs[2].EchoMode != 1 { // textinput.EchoPassword
		t.Fatal("the value input must be masked")
	}

	m = commitCredential(m, 0, "Stripe", "STRIPE_KEY", "sk-live-1")
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	if len(m.credentials) != 1 || m.credentials[0].Name != "stripe" || m.credentials[0].Kind != "env" {
		t.Fatalf("declaration = %+v (the name lowercases itself)", m.credentials)
	}
	if string(m.stagedCredValues["stripe"]) != "sk-live-1" {
		t.Fatal("the typed value must stage under the declared name")
	}
	if !m.dirty() {
		t.Fatal("staging must flip dirty")
	}
	// The staged value renders NOWHERE: not in the row, not in the sig.
	for _, r := range m.credentialRows() {
		if strings.Contains(r.text, "sk-live-1") {
			t.Fatalf("value leaked into a row: %q", r.text)
		}
	}
	if strings.Contains(m.sig(), "sk-live-1") {
		t.Fatal("value leaked into the dirty signature")
	}
	// Value-state cell: staged.
	if rows := m.credentialRows(); !strings.Contains(rows[0].text, "(staged)") {
		t.Fatalf("row = %q, want the staged cell", rows[0].text)
	}
}

func TestCredentialCommitValidatesShapeAndValue(t *testing.T) {
	m, _ := credTestModel(t, config.Config{})
	if r := commitCredential(m, 0, "bad name", "X", ""); !strings.Contains(r.itemErr, "lowercase") {
		t.Fatalf("bad name: %q", r.itemErr)
	}
	if r := commitCredential(m, 0, "a", "BYRE_EGRESS", ""); !strings.Contains(r.itemErr, "BYRE_ namespace") {
		t.Fatalf("reserved target: %q", r.itemErr)
	}
	// An env value over the cap is refused at the form, where re-entry is
	// cheap. (A NUL can't arrive here at all — the textinput sanitizes
	// control runes on entry; the CLI's pipe path validates it instead.)
	if r := commitCredential(m, 0, "a", "A", strings.Repeat("x", credentials.MaxEnvValue+1)); !strings.Contains(r.itemErr, "cap") {
		t.Fatalf("oversize env value: %q", r.itemErr)
	}
}

func TestCredentialSaveFlushesToVault(t *testing.T) {
	m, store := credTestModel(t, config.Config{})
	if err := credentials.Open(store, "test-project-id").Create("pw"); err != nil {
		t.Fatal(err)
	}
	m = m.loadConfig(m.base) // reset staged after vault creation probe
	m.credVault = credentials.Open(store, "test-project-id")
	m = commitCredential(m, 0, "stripe", "STRIPE_KEY", "sk-live-2")
	m = m.save()
	if m.errMsg != "" {
		t.Fatalf("save: %s", m.errMsg)
	}
	if len(m.stagedCredValues) != 0 {
		t.Fatal("staged values must clear on a successful flush")
	}
	if !strings.Contains(m.status, "1 credential value") {
		t.Fatalf("status = %q, want the flush count", m.status)
	}
	if m.dirty() {
		t.Fatal("a full save must leave the model clean")
	}
	// The vault holds the value, encrypted, stamped for this project.
	u, err := credentials.Open(store, "test-project-id").Unlock("pw")
	if err != nil {
		t.Fatal(err)
	}
	val, oc, derr := u.Decrypt("stripe")
	if oc != "" || derr != nil || string(val) != "sk-live-2" {
		t.Fatalf("vault roundtrip: %s %v %q", oc, derr, val)
	}
	// The config file carries the declaration and never the value.
	raw, err := os.ReadFile(m.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `name = "stripe"`) || strings.Contains(string(raw), "sk-live-2") {
		t.Fatalf("config file:\n%s", raw)
	}
	// Value-state moved staged -> set.
	if rows := m.credentialRows(); !strings.Contains(rows[0].text, "(set)") {
		t.Fatalf("row = %q, want set after flush", rows[0].text)
	}
}

func TestCredentialSaveVaultlessOpensPassModalAndCreates(t *testing.T) {
	m, store := credTestModel(t, config.Config{})
	m = commitCredential(m, 0, "stripe", "STRIPE_KEY", "sk-live-3")
	m = m.save()
	if m.mode != modeCredPass {
		t.Fatalf("vault-less save with staged values must open the passphrase modal; mode = %v", m.mode)
	}
	// Nothing written yet: the modal runs before any disk write.
	if _, err := os.Stat(m.filePath); !os.IsNotExist(err) {
		t.Fatal("cancel-able modal must precede the config write")
	}
	// Mismatch then match: the modal validates in place.
	m.credPassInputs[0].SetValue("pw")
	m.credPassInputs[1].SetValue("other")
	m.credPassFocus = 1
	mm, _ := m.updateCredPass(keyMsg("enter"))
	m = mm.(model)
	if !strings.Contains(m.credPassErr, "do not match") {
		t.Fatalf("mismatch: %q", m.credPassErr)
	}
	m.credPassInputs[1].SetValue("pw")
	mm, _ = m.updateCredPass(keyMsg("enter"))
	m = mm.(model)
	if m.errMsg != "" || m.mode != modeForm {
		t.Fatalf("confirm: err=%q mode=%v", m.errMsg, m.mode)
	}
	v := credentials.Open(store, "test-project-id")
	if !v.Exists() {
		t.Fatal("the vault must exist after the inline creation")
	}
	u, err := v.Unlock("pw")
	if err != nil {
		t.Fatalf("inline-created vault unlock: %v", err)
	}
	if val, oc, _ := u.Decrypt("stripe"); oc != "" || string(val) != "sk-live-3" {
		t.Fatalf("value after inline create: %s %q", oc, val)
	}
	if m.credPassphrase != "" {
		t.Fatal("the passphrase must not persist in the model after the flush")
	}
}

func TestCredentialFlushFailureKeepsDirty(t *testing.T) {
	// A failed vault flush leaves the staged values as UNSAVED work: dirty
	// must stay true (the quit confirm and the ^e clean-state gate key on
	// it), or a quit would silently discard the values (codex, screen
	// review round 1). Failure induced by a guard that refuses the vault
	// write after the config write succeeded.
	m, _ := credTestModel(t, config.Config{})
	if err := credentials.Open(filepath.Dir(m.filePath), "test-project-id").Create("pw"); err != nil {
		t.Fatal(err)
	}
	m.credVault = credentials.Open(filepath.Dir(m.filePath), "test-project-id")
	m = commitCredential(m, 0, "stripe", "STRIPE_KEY", "v")
	calls := 0
	m.guard = func(do func() error) error {
		calls++
		if calls == 1 {
			return do() // the config write succeeds
		}
		return os.ErrPermission // the vault flush fails
	}
	m = m.save()
	if !strings.Contains(m.errMsg, "still unsaved") {
		t.Fatalf("errMsg = %q", m.errMsg)
	}
	if len(m.stagedCredValues) != 1 {
		t.Fatal("the staged value must survive a failed flush")
	}
	if !m.dirty() {
		t.Fatal("dirty must stay true after a failed flush — quit would silently discard the staged value")
	}
}

func TestCredentialPartialFlushUnstagesWhatLanded(t *testing.T) {
	// A mid-loop Set failure must leave the truth on both sides: values
	// written before it are stored and unstaged; the failing one stays
	// staged, is named in the error, and keeps the model dirty. The flush
	// iterates sorted, so "a-ok before z-bad" is a contract, not luck. The
	// failure is induced through Set's own kind validation: z-bad is
	// declared env but its staged bytes exceed the env cap (stageable here
	// only by writing the map directly — the item editor validates this at
	// entry, which is exactly why the flush must too).
	m, store := credTestModel(t, config.Config{})
	if err := credentials.Open(store, "test-project-id").Create("pw"); err != nil {
		t.Fatal(err)
	}
	m.credVault = credentials.Open(store, "test-project-id")
	m = commitCredential(m, 0, "a-ok", "A_OK", "fine")
	m = commitCredential(m, 0, "z-bad", "Z_BAD", "placeholder")
	m.stagedCredValues["z-bad"] = bytes.Repeat([]byte("x"), credentials.MaxEnvValue+1)
	m = m.save()
	if !strings.Contains(m.errMsg, "credential z-bad") || !strings.Contains(m.errMsg, "1 staged credential value still unsaved") {
		t.Fatalf("errMsg = %q — must name the failing credential and the true remaining count", m.errMsg)
	}
	if len(m.stagedCredValues) != 1 || m.stagedCredValues["z-bad"] == nil {
		t.Fatalf("staged after partial flush = %v — only the failure may remain", keysOf(m.stagedCredValues))
	}
	if !m.credStoredNames["a-ok"] {
		t.Fatal("the value that landed must show stored")
	}
	if !m.dirty() {
		t.Fatal("a partial flush leaves unsaved work; dirty must stay true")
	}
	// The landed value is really in the vault.
	u, err := credentials.Open(store, "test-project-id").Unlock("pw")
	if err != nil {
		t.Fatal(err)
	}
	if val, oc, _ := u.Decrypt("a-ok"); oc != "" || string(val) != "fine" {
		t.Fatalf("landed value: %s %q", oc, val)
	}
}

func TestCredentialPassModalCancelKeepsStaged(t *testing.T) {
	m, store := credTestModel(t, config.Config{})
	m = commitCredential(m, 0, "stripe", "STRIPE_KEY", "v")
	m = m.save()
	mm, _ := m.updateCredPass(keyMsg("esc"))
	m = mm.(model)
	if m.mode != modeForm || len(m.stagedCredValues) != 1 || !m.dirty() {
		t.Fatalf("cancel must keep the values staged and the model dirty (mode=%v staged=%d)", m.mode, len(m.stagedCredValues))
	}
	if v := credentials.Open(store, "test-project-id"); v.Exists() {
		t.Fatal("cancel must create nothing")
	}
}

func TestCredentialDeleteDropsStagedValue(t *testing.T) {
	m, _ := credTestModel(t, config.Config{})
	m = commitCredential(m, 0, "stripe", "STRIPE_KEY", "v")
	m.listField = fCredentials
	m.deleteItem(fCredentials, 0)
	if len(m.stagedCredValues) != 0 {
		t.Fatal("deleting the declaration must drop its staged value")
	}
}

func TestCredentialGlobalEditorDeclaresOnly(t *testing.T) {
	m := newModel("t", "/tmp/default.config", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetGlobal)
	m.listField = fCredentials
	m = m.startItem(-1)
	if len(m.inputs) != 2 {
		t.Fatalf("global add form inputs = %d, want 2 (no value input: the vault is project-scoped)", len(m.inputs))
	}
	m.inputs[0].SetValue("stripe")
	m.inputs[1].SetValue("STRIPE_KEY")
	m = m.commitItem()
	if m.itemErr != "" || len(m.credentials) != 1 {
		t.Fatalf("global declare: %q %+v", m.itemErr, m.credentials)
	}
	// No value-state cell without a vault surface.
	if rows := m.credentialRows(); strings.Contains(rows[0].text, "(unset)") {
		t.Fatalf("global row must not claim value-state: %q", rows[0].text)
	}
}

func TestCredentialRejectedCommitLeavesNoStagedValue(t *testing.T) {
	// A commit the layer check rejects must revert WHOLE, staged value
	// included: a shared map would keep the secret staged behind orig's
	// back and a later ^s would encrypt an orphan with no declaration
	// (grok, screen review round 1).
	m, _ := credTestModel(t, config.Config{})
	m = commitCredential(m, 0, "a", "SHARED_TARGET", "")
	if m.itemErr != "" {
		t.Fatal(m.itemErr)
	}
	// Second credential colliding on the target: ValidateLayer rejects at
	// the commit tail, after the arm staged the value.
	m = commitCredential(m, 0, "b", "SHARED_TARGET", "orphan-secret")
	if !strings.Contains(m.itemErr, "collides") {
		t.Fatalf("itemErr = %q, want the target-collision rule", m.itemErr)
	}
	if len(m.credentials) != 1 {
		t.Fatalf("declarations must revert: %+v", m.credentials)
	}
	if len(m.stagedCredValues) != 0 {
		t.Fatal("the rejected commit's value must not stay staged")
	}
	if m.credStagedGen != 0 {
		t.Fatal("the staged generation must revert with the model copy")
	}
}

func TestCredentialExposureTallyTracksWorkingSet(t *testing.T) {
	// The exposure headline must move with the session's edits, not the
	// open-time file (grok, screen review round 1).
	m, _ := credTestModel(t, config.Config{})
	if got := len(m.credentialsNow()); got != 0 {
		t.Fatalf("fresh model credentials tally = %d", got)
	}
	m = commitCredential(m, 0, "stripe", "STRIPE_KEY", "")
	if got := len(m.credentialsNow()); got != 1 {
		t.Fatalf("tally after add = %d, want 1 (the working set, not m.base)", got)
	}
	m.listField = fCredentials
	m.deleteItem(fCredentials, 0)
	if got := len(m.credentialsNow()); got != 0 {
		t.Fatalf("tally after delete = %d, want 0", got)
	}
}

func TestCredentialRenameOfStoredValueSaysSo(t *testing.T) {
	// A stored (vault) value cannot follow a rename — cold writes can't
	// re-stamp ciphertext — so the commit says so instead of letting the
	// (unset) cell be the only clue.
	m, store := credTestModel(t, config.Config{})
	if err := credentials.Open(store, "test-project-id").Create("pw"); err != nil {
		t.Fatal(err)
	}
	v := credentials.Open(store, "test-project-id")
	if err := v.Set("stripe", []byte("sk"), "env"); err != nil {
		t.Fatal(err)
	}
	m.credVault = v
	m.credStoredNames = map[string]bool{"stripe": true}
	m = commitCredential(m, 0, "stripe", "STRIPE_KEY", "")
	m.listField = fCredentials
	m = m.startItem(0)
	m.inputs[0].SetValue("payments")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatal(m.itemErr)
	}
	if !strings.Contains(m.status, "stays under stripe") {
		t.Fatalf("rename-of-stored notice missing: %q", m.status)
	}
}

func TestCredentialRenameCarriesStagedValue(t *testing.T) {
	m, _ := credTestModel(t, config.Config{})
	m = commitCredential(m, 0, "stripe", "STRIPE_KEY", "v1")
	// Rename via edit, no new value typed: the staged value follows.
	m.listField = fCredentials
	m = m.startItem(0)
	m.inputs[0].SetValue("payments")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatal(m.itemErr)
	}
	if string(m.stagedCredValues["payments"]) != "v1" || len(m.stagedCredValues) != 1 {
		t.Fatalf("staged after rename = %v", keysOf(m.stagedCredValues))
	}
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
