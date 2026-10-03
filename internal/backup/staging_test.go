package backup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func newTestStaging(t *testing.T, home, runID string) *Staging {
	t.Helper()
	st, err := NewStaging(home, runID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Remove() })
	return st
}

func TestStagingIsPrivateAndPositional(t *testing.T) {
	home := t.TempDir()
	st := newTestStaging(t, home, "aabbccdd")

	fi, err := os.Lstat(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("staging directory mode = %o, want 0700", fi.Mode().Perm())
	}
	if st.Path() != filepath.Join(home, StagingDirName, "aabbccdd") {
		t.Fatalf("Path() = %q", st.Path())
	}

	f, err := st.Create("0.tar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("payload"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	fi, err = os.Lstat(filepath.Join(st.Path(), "0.tar"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("payload mode = %o, want 0600", fi.Mode().Perm())
	}

	// Create is exclusive: a name already taken is a refusal, not a silent
	// truncation of someone else's bytes.
	if _, err := st.Create("0.tar"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second Create err = %v, want fs.ErrExist", err)
	}

	r, err := st.Open("0.tar")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got := make([]byte, 7)
	if _, err := r.Read(got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("read back %q", got)
	}
}

func TestStagingRefusesANameThatIsNotPositional(t *testing.T) {
	st := newTestStaging(t, t.TempDir(), "aabbccdd")
	cases := []struct{ name, fragment string }{
		{"", "no member name given"},
		{".", `member "." is not a plain name`},
		{"..", `member ".." is not a plain name`},
		{"sub/0.tar", `member "sub/0.tar" is not a plain name`},
		{"/etc/passwd", `member "/etc/passwd" is not a plain name`},
		{"../escape", `member "../escape" is not a plain name`},
	}
	for _, c := range cases {
		_, err := st.Create(c.name)
		wantRule(t, err, c.fragment)
		_, err = st.Open(c.name)
		wantRule(t, err, c.fragment)
	}
}

func TestStagingRemoveLeavesNothing(t *testing.T) {
	home := t.TempDir()
	st, err := NewStaging(home, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"0.tar", "1.tar", "2.tar"} {
		f, err := st.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if err := st.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(st.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("staging still present: %v", err)
	}
	// Removed twice is how a verb with both a defer and an explicit cleanup
	// behaves; it is not an error.
	if err := st.Remove(); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func TestStagingRunsAreDisjoint(t *testing.T) {
	home := t.TempDir()
	one := newTestStaging(t, home, "1111aaaa")
	two := newTestStaging(t, home, "2222bbbb")
	if one.Path() == two.Path() {
		t.Fatal("two run ids share one staging directory")
	}
	f, err := one.Create("0.tar")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	// The second run's directory is its own: the first run's member is not
	// visible there, so one verb can never read or overwrite the other's.
	if _, err := two.Open("0.tar"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the other run's member was reachable: %v", err)
	}
	if _, err := NewStaging(home, "1111aaaa"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("reusing a run id was allowed: %v", err)
	}
}

func TestStaleStagingNamesOtherRunsOnly(t *testing.T) {
	home := t.TempDir()
	mine := newTestStaging(t, home, "mine0000")
	other := newTestStaging(t, home, "other000")

	stale, err := StaleStaging(home, "mine0000")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != other.Path() {
		t.Fatalf("StaleStaging = %v, want just %q", stale, other.Path())
	}
	for _, s := range stale {
		if s == mine.Path() {
			t.Fatal("StaleStaging named this run's own directory")
		}
	}
	// Nothing is removed: byre cannot tell a crash's leftovers from a verb
	// still running, so it names one and leaves it.
	if _, err := os.Lstat(other.Path()); err != nil {
		t.Fatalf("StaleStaging removed what it named: %v", err)
	}
}

func TestStaleStagingOnAFirstRun(t *testing.T) {
	stale, err := StaleStaging(t.TempDir(), "whatever")
	if err != nil {
		t.Fatalf("a missing staging directory is the ordinary first run: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("StaleStaging = %v, want nothing", stale)
	}
}

func TestStagingFreeBytes(t *testing.T) {
	st := newTestStaging(t, t.TempDir(), "aabbccdd")
	free, err := st.FreeBytes()
	if err != nil {
		t.Fatal(err)
	}
	if free == 0 {
		t.Fatal("FreeBytes = 0 on a writable filesystem")
	}
}

func TestNewStagingRefusesABadRunID(t *testing.T) {
	home := t.TempDir()
	cases := []struct{ id, fragment string }{
		{"", "no run id given"},
		{".", `run id "." is not a plain name`},
		{"..", `run id ".." is not a plain name`},
		{"a/b", `run id "a/b" is not a plain name`},
		{"/abs", `run id "/abs" is not a plain name`},
	}
	for _, c := range cases {
		_, err := NewStaging(home, c.id)
		wantRule(t, err, c.fragment)
	}
	_, err := NewStaging("", "aabbccdd")
	wantRule(t, err, "no byre home given")
	_, err = StaleStaging("", "aabbccdd")
	wantRule(t, err, "no byre home given")
}
