package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// makeWorktree fabricates git's on-disk worktree layout (no git binary needed):
// a main repo with a real .git dir, a worktrees/<name> git dir with a commondir
// file, and a worktree whose .git is a pointer file. It returns the main and
// worktree paths.
func makeWorktree(t *testing.T, name string) (main, wt string) {
	t.Helper()
	root := t.TempDir()
	main = filepath.Join(root, "main")
	wt = filepath.Join(root, name)
	gd := filepath.Join(main, ".git", "worktrees", name)
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// commondir is relative (as git writes it): worktrees/<name> -> ../.. = .git
	if err := os.WriteFile(filepath.Join(gd, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return main, wt
}

func TestResolveWorktreeInheritsMainIdentity(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	main, wt := makeWorktree(t, "feature")

	mainPaths, err := Resolve(main)
	if err != nil {
		t.Fatal(err)
	}
	wtPaths, err := Resolve(wt)
	if err != nil {
		t.Fatal(err)
	}

	// Identity (id/dir/config store) is inherited from the main worktree.
	if wtPaths.ID != mainPaths.ID {
		t.Fatalf("worktree id %q != main id %q (should inherit)", wtPaths.ID, mainPaths.ID)
	}
	if wtPaths.Dir != mainPaths.Dir || wtPaths.LockFile != mainPaths.LockFile {
		t.Fatalf("worktree store paths not inherited: %+v vs %+v", wtPaths, mainPaths)
	}
	if wtPaths.Canonical != mainPaths.Canonical {
		t.Fatalf("worktree Canonical %q != main %q", wtPaths.Canonical, mainPaths.Canonical)
	}

	// Per-worktree side stays local.
	if !wtPaths.IsWorktree {
		t.Fatal("IsWorktree = false for a linked worktree")
	}
	wantWork, _ := Canonicalize(wt)
	if wtPaths.WorkDir != wantWork {
		t.Fatalf("WorkDir = %q, want %q", wtPaths.WorkDir, wantWork)
	}
	if wtPaths.WorktreeID == wtPaths.ID {
		t.Fatalf("WorktreeID %q must differ from the project ID for a worktree", wtPaths.WorktreeID)
	}
	wantCommon, _ := Canonicalize(filepath.Join(main, ".git"))
	if gotCommon, _ := Canonicalize(wtPaths.CommonGitDir); gotCommon != wantCommon {
		t.Fatalf("CommonGitDir = %q, want %q", wtPaths.CommonGitDir, wantCommon)
	}

	// The main worktree itself is not flagged as one, and its local fields mirror
	// identity.
	if mainPaths.IsWorktree {
		t.Fatal("main worktree wrongly detected as a linked worktree")
	}
	if mainPaths.WorkDir != mainPaths.Canonical || mainPaths.WorktreeID != mainPaths.ID {
		t.Fatal("plain project should have WorkDir==Canonical and WorktreeID==ID")
	}
}

func TestResolveSubmoduleIsStandalone(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	root := t.TempDir()
	sub := filepath.Join(root, "super", "sub")
	// A submodule's git dir lives under .git/modules and has NO commondir file.
	gd := filepath.Join(root, "super", ".git", "modules", "sub")
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// git writes a RELATIVE pointer for submodules.
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := Resolve(sub)
	if err != nil {
		t.Fatalf("submodule resolve errored: %v", err)
	}
	if paths.IsWorktree {
		t.Fatal("submodule wrongly detected as a worktree")
	}
}

func TestResolvePlainRepoAndNonRepoStandalone(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	// Plain repo: .git is a real directory.
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, err := Resolve(repo); err != nil || p.IsWorktree {
		t.Fatalf("plain repo: IsWorktree=%v err=%v", p.IsWorktree, err)
	}
	// Non-repo: no .git at all.
	if p, err := Resolve(t.TempDir()); err != nil || p.IsWorktree {
		t.Fatalf("non-repo: IsWorktree=%v err=%v", p.IsWorktree, err)
	}
}

func TestResolveDanglingWorktreeErrors(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	root := t.TempDir()
	wt := filepath.Join(root, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// Points into a worktrees/<name> dir that does not exist (moved main repo).
	dead := filepath.Join(root, "gone", ".git", "worktrees", "wt")
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+dead+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(wt)
	if err == nil {
		t.Fatal("expected an error for dangling worktree metadata, got nil (would silently mint a standalone identity)")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("error should mention worktree/repair, got: %v", err)
	}
}

// Security: a FORGED .git + commondir must never become an arbitrary rw host
// mount. The agent (project mounted rw) plants a project-local .git pointing
// at a worktrees/<name> dir it controls, whose commondir names an unrelated
// host dir it wants mounted — even with a consistent back-pointer. The
// structural check (gitDir must be <common>/worktrees/<name>) must reject it.
func TestDetectWorktreeRejectsForgedCommondir(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "secret") // the dir the attacker wants bound
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(root, "proj")
	gd := filepath.Join(proj, "fake", "worktrees", "x") // agent-writable
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "commondir"), []byte(secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A consistent back-pointer too — the agent can forge this; the structural
	// check must still catch the escape.
	if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(filepath.Join(proj, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".git"), []byte("gitdir: "+gd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, ok, err := detectWorktree(proj)
	if err == nil {
		t.Fatalf("forged commondir accepted (ok=%v) — would mount %q rw", ok, info.commonGitDir)
	}
	if info.commonGitDir == secret {
		t.Fatalf("forged commondir leaked as the mount target: %q", secret)
	}
}

// The mounted common git dir must be byre's own structural path, never the
// raw commondir-file content — even when that content is a symlink that
// resolves correctly at check time. Returning the symlink would hand an
// agent-retargetable path to the RW bind mount (a check-to-mount race). The
// structural check passes here (the symlink resolves to the true common dir),
// so this pins the RETURNED path, not rejection.
func TestDetectWorktreeMountsStructuralPathNotSymlinkedCommondir(t *testing.T) {
	main, wt := makeWorktree(t, "wt")
	realCommon := filepath.Join(main, ".git")
	// A symlink that currently resolves to the true common dir; its PATH is
	// what we plant as commondir content.
	link := filepath.Join(t.TempDir(), "commonlink")
	if err := os.Symlink(realCommon, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	gd := filepath.Join(realCommon, "worktrees", "wt")
	if err := os.WriteFile(filepath.Join(gd, "commondir"), []byte(link+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, ok, err := detectWorktree(wt)
	if err != nil || !ok {
		t.Fatalf("a symlinked-but-consistent commondir should still detect: ok=%v err=%v", ok, err)
	}
	if info.commonGitDir == link {
		t.Fatalf("commonGitDir is the attacker-controlled symlink path %q — retargetable before the mount resolves", link)
	}
	if info.commonGitDir != realCommon {
		t.Fatalf("commonGitDir = %q, want the structural path %q", info.commonGitDir, realCommon)
	}
}

// The mount SOURCE (commonGitDirHost) must be symlink-free even when the
// git-recorded path itself routes through a symlink — the .git pointer is
// attacker-controlled, so a symlink COMPONENT of gitDir (not just a symlinked
// commondir value) is equally retargetable between validation and mount.
// commonGitDirHost is Canonicalize'd, so it must resolve every component; the
// target stays the git-recorded path so in-box pointers still resolve.
func TestDetectWorktreeHostPathResolvesSymlinkedGitDir(t *testing.T) {
	root := t.TempDir()
	realBase := filepath.Join(root, "realbase")
	gd := filepath.Join(realBase, ".git", "worktrees", "wt")
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink standing in for realBase; the .git pointer routes gitDir
	// THROUGH it, so structCommon = <link>/.git has a symlink component.
	link := filepath.Join(root, "link")
	if err := os.Symlink(realBase, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	gdViaLink := filepath.Join(link, ".git", "worktrees", "wt")
	if err := os.WriteFile(filepath.Join(gd, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gdViaLink+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, ok, err := detectWorktree(wt)
	if err != nil || !ok {
		t.Fatalf("a worktree reached via a symlinked gitDir should detect: ok=%v err=%v", ok, err)
	}
	// Target: the git-recorded path (routes through the symlink) — unchanged so
	// in-box pointers resolve.
	wantTarget := filepath.Join(link, ".git")
	if info.commonGitDir != wantTarget {
		t.Fatalf("commonGitDir (target) = %q, want git-recorded %q", info.commonGitDir, wantTarget)
	}
	// Source: fully resolved — no symlink component survives for a retarget.
	wantHost, _ := Canonicalize(filepath.Join(realBase, ".git"))
	if info.commonGitDirHost != wantHost {
		t.Fatalf("commonGitDirHost (mount source) = %q, want symlink-free %q", info.commonGitDirHost, wantHost)
	}
	if strings.Contains(info.commonGitDirHost, string(filepath.Separator)+"link"+string(filepath.Separator)) {
		t.Fatalf("commonGitDirHost still routes through the symlink: %q", info.commonGitDirHost)
	}
}

// A project .git that is itself a SYMLINK is never treated as a worktree
// pointer: following it (ReadFile + the reciprocal Stat) would let an agent
// point .git at genuine external worktree metadata and pass every check,
// turning it into an arbitrary rw host mount. It must resolve to standalone.
func TestDetectWorktreeRejectsSymlinkedDotGit(t *testing.T) {
	// A genuine external worktree (valid, consistent metadata).
	extMain, extWT := makeWorktree(t, "wt")
	_ = extMain
	// The victim project: its .git is a SYMLINK to the genuine worktree's .git.
	proj := t.TempDir()
	if err := os.Symlink(filepath.Join(extWT, ".git"), filepath.Join(proj, ".git")); err != nil {
		t.Fatal(err)
	}
	info, ok, err := detectWorktree(proj)
	if err != nil {
		t.Fatalf("a symlinked .git must resolve to standalone, not error: %v", err)
	}
	if ok || info.commonGitDir != "" {
		t.Fatalf("symlinked .git was treated as a worktree: ok=%v mount=%q", ok, info.commonGitDir)
	}
}

// detectWithTimeout runs detectWorktree and fails the test if it blocks: the
// regression mode for FIFO/device-backed metadata is an indefinite hang (or an
// unbounded read), so plain test hangs would mask exactly the bug class these
// tests pin.
func detectWithTimeout(t *testing.T, dir string) (worktreeInfo, bool, error) {
	t.Helper()
	type result struct {
		info worktreeInfo
		ok   bool
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, ok, err := detectWorktree(dir)
		done <- result{info, ok, err}
	}()
	select {
	case r := <-done:
		return r.info, r.ok, r.err
	case <-time.After(15 * time.Second):
		t.Fatal("detectWorktree blocked — O_NONBLOCK / size-cap regression")
		return worktreeInfo{}, false, nil // unreachable
	}
}

// A .git that is a FIFO (an agent can swap it in) must not block detection:
// O_NONBLOCK returns immediately and the regular-file gate resolves standalone.
func TestDetectWorktreeDotGitFifoDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, ".git"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	_, ok, err := detectWithTimeout(t, dir)
	if err != nil {
		t.Fatalf("a FIFO .git must resolve standalone, not error: %v", err)
	}
	if ok {
		t.Fatal("a FIFO .git was treated as a worktree")
	}
}

// A .git whose apparent size is implausibly large (an agent can create a huge
// SPARSE regular file, which passes the regular-file gate) must resolve
// standalone without attempting to read it all — read-to-EOF on the apparent
// size is a host OOM.
func TestDetectWorktreeOversizedDotGitIsStandalone(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	// A valid pointer prefix, then sparse-extended: proves the cap rejects on
	// size rather than the content merely failing to parse.
	if _, err := f.WriteString("gitdir: /tmp/x/worktrees/y\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxMetaFileSize + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, ok, err := detectWithTimeout(t, dir)
	if err != nil {
		t.Fatalf("an oversized .git must resolve standalone, not error: %v", err)
	}
	if ok {
		t.Fatal("an oversized .git was treated as a worktree")
	}
}

// The worktree metadata files (commondir, gitdir) live in a directory named by
// agent-writable content, so they get the same hostile-filesystem treatment as
// .git: a FIFO, a symlink (even one pointing at valid content), or an
// implausibly large file must be a loud error — never a hang, an unbounded
// read, or an accepted worktree.
func TestDetectWorktreeHostileMetadataFiles(t *testing.T) {
	sabotages := []struct {
		name  string
		plant func(t *testing.T, path string, valid []byte)
	}{
		{"fifo", func(t *testing.T, path string, _ []byte) {
			if err := syscall.Mkfifo(path, 0o644); err != nil {
				t.Skipf("mkfifo unavailable: %v", err)
			}
		}},
		{"symlink-to-dev-zero", func(t *testing.T, path string, _ []byte) {
			if err := os.Symlink("/dev/zero", path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
		{"symlink-to-valid-content", func(t *testing.T, path string, valid []byte) {
			// Even a symlink to a regular file holding the GENUINE content is
			// rejected: git never writes these as symlinks, and following one
			// re-opens the hostile-target problem.
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, valid, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
		{"oversized", func(t *testing.T, path string, valid []byte) {
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(valid); err != nil {
				t.Fatal(err)
			}
			if err := f.Truncate(maxMetaFileSize + 1); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}},
	}
	for _, target := range []string{"commondir", "gitdir"} {
		for _, s := range sabotages {
			t.Run(target+"/"+s.name, func(t *testing.T) {
				main, wt := makeWorktree(t, "wt")
				path := filepath.Join(main, ".git", "worktrees", "wt", target)
				valid, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				s.plant(t, path, valid)
				info, ok, err := detectWithTimeout(t, wt)
				if err == nil {
					t.Fatalf("hostile %s (%s) accepted: ok=%v mount=%q", target, s.name, ok, info.commonGitDir)
				}
			})
		}
	}
}

// readMetaFile's contract, pinned directly: regular files read fine, and each
// hostile shape maps to its distinguishable rejection.
func TestReadMetaFile(t *testing.T) {
	t.Run("regular", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(path, []byte("content\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		data, info, err := readMetaFile(path)
		if err != nil || string(data) != "content\n" || info == nil {
			t.Fatalf("readMetaFile = (%q, %v, %v)", data, info, err)
		}
	})
	t.Run("fifo", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "f")
		if err := syscall.Mkfifo(path, 0o644); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			_, _, err := readMetaFile(path)
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, errMetaNotRegular) {
				t.Fatalf("want errMetaNotRegular for a FIFO, got %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("readMetaFile blocked on a FIFO — O_NONBLOCK regression")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "link")
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, _, err := readMetaFile(path); !errors.Is(err, syscall.ELOOP) {
			t.Fatalf("want ELOOP for a symlink, got %v", err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "f")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(maxMetaFileSize + 1); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if _, _, err := readMetaFile(path); !errors.Is(err, errMetaTooLarge) {
			t.Fatalf("want errMetaTooLarge, got %v", err)
		}
	})
}

// The reciprocal back-pointer is validated too: a structurally-plausible
// worktree whose <gitDir>/gitdir does NOT point back at <dir>/.git is rejected.
func TestDetectWorktreeRejectsBadBackPointer(t *testing.T) {
	main, wt := makeWorktree(t, "wt")
	gd := filepath.Join(main, ".git", "worktrees", "wt")
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(other+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := detectWorktree(wt); err == nil {
		t.Fatal("a worktree whose back-pointer names an unrelated file must be rejected")
	}
}

func TestParseGitdirPointer(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"gitdir: /abs/path\n", "/abs/path", true},
		{"gitdir: ../rel/path", "../rel/path", true},
		{"gitdir: /has space/x\n", "/has space/x", true},
		{"ref: refs/heads/main\n", "", false},
		{"gitdir: \n", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := parseGitdirPointer(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseGitdirPointer(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
