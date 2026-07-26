package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
)

// The exit report: what byre noticed changing, in a small fixed set of places
// the HOST runs code from, while a session was running (ADR 0047).
//
// The project mount is the one grant every box has, and the project is a
// directory host tools execute: a git hook, a git config naming a program, an
// env file steering a later process. A host-side git that reaches one of those
// runs it, as the user. Most of what an agent writes lands in the diff,
// where reviewing it is already the user's job -- these places do not, so byre
// points them out.
//
// This is NOT containment and must never be worded as if it were. byre is not a
// security product; against an actively malicious agent it takes some simple
// precautions and claims nothing more. The watch set is deliberately small, it
// is incomplete by construction, and the report says so.

// maxEnvFileBytes bounds a read of an agent-writable env file. Real .env files
// are hundreds of bytes; this is pure slack, and the cap is what keeps a device
// node or a planted symlink from streaming into host byre.
const maxEnvFileBytes = 1 << 20 // 1 MiB

// maxHooksEntries bounds the hooks walk. A stock hooks directory holds ~14
// samples. The cap matters for a core.hooksPath redirected at a large in-tree
// directory: byre reports the redirect either way (it is an exec-relevant key),
// so truncating the walk costs nothing that matters.
const maxHooksEntries = 500

// execRelevantConfig reports whether a git config key can put a program on the
// host's path of execution. It is RANKING, not gating: its job is to keep the
// report rare enough to be read, by staying silent on the config churn that
// ends an ordinary session (branch.*.remote from `push -u`, remote.*.url from
// `remote add`, the filter.lfs.* that `git lfs install` writes).
//
// It is incomplete by construction and that is fine -- ADR 0009 rejected an
// exec-capable-key allowlist as a GATE, where incompleteness fails silently.
// Here incompleteness just means byre says less, and the report's own wording
// ("a handful of places, not everything") never claims otherwise.
//
// Keys arrive from `git config --list` already lowercased in section and key
// (subsection case is preserved, hence the suffix matching rather than exact).
func execRelevantConfig(key, value string) bool {
	switch key {
	case "core.hookspath", "core.fsmonitor", "core.sshcommand", "core.pager",
		"core.editor", "credential.helper", "init.templatedir", "gpg.program",
		"ssh.variant", "include.path":
		return true
	}
	// alias.<name> only when it shells out: `!sh -c ...`. A plain alias
	// (alias.co = checkout) is ordinary configuration and stays quiet.
	if strings.HasPrefix(key, "alias.") {
		return strings.HasPrefix(strings.TrimSpace(value), "!")
	}
	// git-lfs writes filter.lfs.{clean,smudge,process} on `git lfs install`,
	// in every LFS repo, as ordinary setup. Ranking it would break the silence
	// this function exists to protect -- and LFS also installs hooks, which DO
	// speak, so the session is not silent about it either way.
	if strings.HasPrefix(key, "filter.lfs.") {
		return false
	}
	// Subsectioned forms: filter.<name>.smudge, diff.<name>.textconv,
	// credential.<url>.helper, url.<name>.insteadof, includeif.<cond>.path.
	for _, suffix := range []string{
		".clean", ".smudge", ".process", ".textconv", ".command",
		".helper", ".insteadof", ".path",
	} {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		switch {
		case strings.HasPrefix(key, "filter."),
			strings.HasPrefix(key, "diff."),
			strings.HasPrefix(key, "credential."),
			strings.HasPrefix(key, "url."),
			strings.HasPrefix(key, "includeif."):
			return true
		}
	}
	return false
}

// configValueShown reports whether a git config value is safe to echo.
//
// Path-like destinations are the whole point of the message -- "core.hooksPath
// is now .husky/_" tells you where your hooks come from, and a path is not a
// secret. Command- and helper-shaped values are a different matter: a
// `credential.helper` or a `!` alias is a shell snippet that routinely embeds a
// token, and `url.<...>.insteadOf` carries userinfo. Those get NAMED, never
// quoted -- the same rule `.env` follows, for the same reason: the terminal,
// scrollback and any captured log are all downstream of this line.
func configValueShown(key string) bool {
	switch key {
	case "core.hookspath", "init.templatedir":
		return true
	}
	return false
}

// redactKeyUserinfo strips credentials that live in a git config KEY rather
// than its value. Suppressing values alone does not close the channel: real
// shapes put the secret in the subsection --
// `url.https://TOKEN@example.com/.insteadOf`,
// `credential.https://user:pass@host.helper` -- and the key is what names the
// finding. Host and scheme survive (they are what makes the line legible);
// anything before the `@` does not.
//
// Accepted residual: scp-style keys (`url.git@github.com:.insteadOf`) carry no
// `://` and are left verbatim. That shape names a user, not a credential.
func redactKeyUserinfo(key string) string {
	i := strings.Index(key, "://")
	if i < 0 {
		return key
	}
	rest := key[i+3:]
	at := strings.IndexByte(rest, '@')
	if at < 0 {
		return key
	}
	// Userinfo is only userinfo before the path begins.
	if slash := strings.IndexByte(rest, '/'); slash >= 0 && at > slash {
		return key
	}
	return key[:i+3] + "<redacted>@" + rest[at+1:]
}

// exitSnapshot is what the report compares. Each map is keyed by a DISPLAY
// path (what the user will read), so a worktree session naming the main tree's
// git dir cannot be mistaken for something inside its own checkout.
type exitSnapshot struct {
	hooks  map[string]string            // display path -> content signature
	config map[string]map[string]string // display path -> git config key -> value
	env    map[string]map[string]string // display path -> env key -> value
	// hooksWalked records that every hooks directory byre meant to look at was
	// actually walkable. Without it an unstattable .git makes the hooks map
	// empty, and diffHooks reports git's own stock *.sample hooks as removed --
	// the same invented-deletion bug as the config side, on the other half of
	// the watch set (grok).
	hooksWalked bool
	// configFromListing marks config files that exist only because the
	// worktrees/ listing found them. When that listing failed on either side
	// they cannot be compared -- but the common config always can, so the
	// primary signal is never suppressed along with them (codex, grok).
	configFromListing map[string]bool
	// configListed records that the git admin dir's worktrees/ enumeration
	// succeeded. A failed listing hides linked-worktree config files, which
	// would then read as deleted (codex).
	configListed bool
	// envListed records that the project root was successfully enumerated. A
	// failed enumeration is not an empty project: without this, every .env
	// byre had seen would read as deleted (codex).
	envListed bool
	// unreadable marks a watched file that EXISTS but could not be read or
	// parsed (a transient git probe failure, an oversize .env, a planted
	// special file). Without it, absence from the maps is indistinguishable
	// from deletion, and the whole-file-deletion report would announce that
	// every key vanished from a file that is sitting right there (codex).
	unreadable map[string]bool
}

// snapshotExit derives the watch set fresh and reads it. Derived fresh on BOTH
// sides on purpose: a session can create a .env.local, or point core.hooksPath
// somewhere new, and a set captured only at the start would miss it.
//
// Every read rides hostopen through a contained root (CLAUDE.md's rule for
// host-side reads of agent-writable paths), so a planted FIFO or a swapped
// symlink degrades that one entry instead of hanging or ballooning host byre.
// Nothing here blocks or fails a session: an unreadable target just sits out
// the comparison.
func snapshotExit(paths project.Paths) exitSnapshot {
	s := exitSnapshot{
		hooks:             map[string]string{},
		config:            map[string]map[string]string{},
		env:               map[string]map[string]string{},
		unreadable:        map[string]bool{},
		configFromListing: map[string]bool{},
		hooksWalked:       true,
	}

	gitDir := commonGitDirOf(paths)
	if gitDir != "" {
		// Config files: the common config, plus every config.worktree that
		// extensions.worktreeConfig can put exec-capable keys in -- the main
		// worktree's and each linked worktree's admin dir. `git config
		// --worktree core.hooksPath ...` opens the whole channel without ever
		// touching `config`.
		cfgFiles, listed := gitConfigFiles(gitDir)
		s.configListed = listed
		for i, cfg := range cfgFiles {
			if i >= 2 { // beyond the two always-known common-dir files
				s.configFromListing[exitDisplay(paths, cfg)] = true
			}
			if kv, ok := readGitConfig(cfg); ok {
				s.config[exitDisplay(paths, cfg)] = kv
			} else if !confirmedAbsent(cfg) {
				// Present, or byre cannot tell. Either way it is not a
				// confirmed deletion -- a transient git probe failure is the
				// motivating case, and an unstattable parent is the other.
				s.unreadable[exitDisplay(paths, cfg)] = true
			}
		}
		hooksDirs := []string{filepath.Join(gitDir, "hooks")}
		// A redirected core.hooksPath is watched only where it stays inside a
		// tree byre already reaches. Outside that, the redirect is REPORTED
		// (core.hookspath is exec-relevant) but never traversed: the target can
		// be $HOME or /tmp, and walking an arbitrary host directory is a cost
		// and a privacy problem byre has no business taking on.
		if p, ok := containedHooksPath(paths); ok {
			hooksDirs = append(hooksDirs, p)
		}
		for _, dir := range hooksDirs {
			if !snapshotHooks(s.hooks, paths, dir) {
				s.hooksWalked = false
			}
		}
	}

	files, listed := envFiles(paths.WorkDir)
	s.envListed = listed
	for _, f := range files {
		if kv, ok := readEnvKeys(f); ok {
			s.env[exitDisplay(paths, f)] = kv
		} else {
			s.unreadable[exitDisplay(paths, f)] = true // envFiles only lists what it saw
		}
	}
	return s
}

// commonGitDirOf resolves the git admin directory to watch. For a linked
// worktree that is the COMMON dir (the main tree's), not the worktree's own
// `.git` -- which is a FILE holding a gitdir pointer, with no config or hooks
// of its own. Bound rw into every worktree box (runparams.go), so a worktree
// session's writes land where the human's main-tree git will read them.
func commonGitDirOf(paths project.Paths) string {
	if paths.CommonGitDirHost != "" {
		return paths.CommonGitDirHost
	}
	// Standalone checkout: <WorkDir>/.git, and only when it really is a
	// directory. A `.git` file (submodule) or a missing one just means there is
	// no admin dir here to watch -- degrade, never guess.
	dir := filepath.Join(paths.WorkDir, ".git")
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}

// gitConfigFiles lists the config files that can carry exec-capable keys for
// this repository: the common config, the main worktree's config.worktree, and
// each linked worktree's. Missing files are simply absent from the snapshot.
func gitConfigFiles(gitDir string) ([]string, bool) {
	files := []string{
		filepath.Join(gitDir, "config"),
		filepath.Join(gitDir, "config.worktree"),
	}
	entries, err := os.ReadDir(filepath.Join(gitDir, "worktrees"))
	if err != nil {
		// No worktrees/ at all is the ordinary case and perfectly known; any
		// other error means byre cannot see the linked worktrees' configs.
		return files, errors.Is(err, fs.ErrNotExist)
	}
	for _, e := range entries {
		if e.IsDir() {
			files = append(files, filepath.Join(gitDir, "worktrees", e.Name(), "config.worktree"))
		}
	}
	return files, true
}

// readGitConfig reads one config file's key/value pairs using git's OWN parser
// rather than a hand-rolled one: subsections, quoting, continuations and
// includes are git's business, and a parser that got them subtly wrong would
// fail by staying quiet. Rides gitProbe, so it is bounded and time-capped
// against a hostile file and degrades (ok=false) when git is absent or the file
// is unreadable -- an unsolicited probe never blocks a session.
func readGitConfig(path string) (map[string]string, bool) {
	out, err := gitProbe("config", "--file", path, "--list", "-z")
	if err != nil {
		return nil, false
	}
	kv := map[string]string{}
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		// `--list -z` emits "key\nvalue" per NUL-terminated record; a valueless
		// key has no newline at all.
		key, value, _ := strings.Cut(rec, "\n")
		kv[key] = value
	}
	return kv, true
}

// containedHooksPath resolves the hooks directory git will ACTUALLY use for
// this worktree, and returns it only when it stays inside a tree byre already
// reaches. Asking git (`-C <worktree> config --get`) rather than reading config
// files ourselves is the point: hooksPath values in `config` and
// `config.worktree` are ALTERNATIVES under git's precedence, not a set, and a
// relative one resolves against the worktree that owns it. Collecting them all
// watched inactive directories and then claimed "your git runs this" about
// them, which is a definite statement that was sometimes false (both
// reviewers).
//
// Tradeoff, deliberate: unlike reading the repo's own config file, asking git
// also applies the user's GLOBAL and system config. That is the right answer
// to "which directory will git use" -- but it means an in-tree hooks dir named
// by the user's own ~/.gitconfig gets watched, and can produce a line the agent
// had nothing to do with. Passive attribution already covers that, and
// inTreeByIdentity still gates the walk.
func containedHooksPath(paths project.Paths) (string, bool) {
	raw, err := gitProbe("-C", paths.WorkDir, "config", "--get", "core.hooksPath")
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(raw))
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(paths.WorkDir, p)
	}
	p = filepath.Clean(p)
	for _, root := range []string{paths.WorkDir, paths.Canonical} {
		if root != "" && inTreeByIdentity(root, p) {
			return p, true
		}
	}
	return "", false
}

// confirmedAbsent reports that a path is DEFINITELY not there. Only ENOENT
// counts: an EACCES on a parent directory means byre cannot tell, and "cannot
// tell" must never become "it was deleted" -- that is the whole point of the
// unreadable state, and treating every Lstat error as absence quietly
// reintroduced the bug it exists to prevent (codex). Never follows a final
// symlink and opens nothing.
func confirmedAbsent(p string) bool {
	_, err := os.Lstat(p)
	return errors.Is(err, fs.ErrNotExist)
}

// snapshotHooks records a signature per file under dir. Enumeration rides a
// contained, no-follow root so the walk cannot be steered outside the directory
// that was selected, and stops at maxHooksEntries.
// Returns false when byre could not tell what is in dir -- a swapped symlink,
// an unstattable parent. A directory that simply is not there is known, not
// unknown, and reports true.
func snapshotHooks(into map[string]string, paths project.Paths, dir string) bool {
	root, err := hostopen.OpenDirRootNoFollow(dir)
	if err != nil {
		return confirmedAbsent(dir)
	}
	defer root.Close()
	n := 0
	fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if n >= maxHooksEntries {
			return fs.SkipAll
		}
		n++
		into[exitDisplay(paths, filepath.Join(dir, p))] = fileSig(root, p, d)
		return nil
	})
	return true
}

// envFiles lists .env and .env.* in the project root. Literal names, never a
// walk: .env.local and .env.development are worth naming, a directory tree is
// not. `.envrc` is deliberately absent -- direnv already gates it (its allow
// record is a hash of path AND content, so an edited .envrc re-blocks until
// `direnv allow` runs), and duplicating that would add noise, not safety.
func envFiles(workDir string) ([]string, bool) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return nil, false
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := e.Name(); n == ".env" || strings.HasPrefix(n, ".env.") {
			out = append(out, filepath.Join(workDir, n))
		}
	}
	sort.Strings(out)
	return out, true
}

// readEnvKeys extracts key names and values from a KEY=value file. Values are
// read ONLY to detect change and never leave this package -- see reportExit.
// Unparseable lines are skipped rather than failing the file: this is a notice,
// not a linter.
func readEnvKeys(path string) (map[string]string, bool) {
	f, fi, err := hostopen.OpenRegular(path, false)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if fi.Size() > maxEnvFileBytes {
		return nil, false
	}
	kv := map[string]string{}
	sc := bufio.NewScanner(io.LimitReader(f, maxEnvFileBytes))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); key != "" {
			kv[key] = value
		}
	}
	if sc.Err() != nil {
		return nil, false
	}
	return kv, true
}

// exitDisplay renders a watched path for the user. Anything under the box's own
// /workspace mount prints repo-relative; anything else -- above all a worktree
// session naming the MAIN tree's git dir -- prints as an abbreviated absolute
// path, so it can never be mistaken for a file in the current checkout.
func exitDisplay(paths project.Paths, p string) string {
	if rel, err := filepath.Rel(paths.WorkDir, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return packages.DisplayPath(p)
}

// reportExit prints what changed between two snapshots. Silent unless something
// worth saying changed -- which is what makes it worth reading when it appears.
//
// Attribution is passive ("these changed"), never "the agent changed": the
// common git dir is shared with concurrent sibling worktree sessions, and the
// user's own host-side git can write it mid-session. reportSelfEditChanges
// makes the same choice for the store, for the same reason.
func reportExit(w io.Writer, before, after exitSnapshot) {
	var lines []string
	lines = append(lines, diffConfig(before, after)...)
	lines = append(lines, diffHooks(before, after)...)
	lines = append(lines, diffEnv(before, after)...)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "⚠ we thought you should know -- a few things changed during this run")
	fmt.Fprintln(w, "  (byre checks a handful of places, not everything):")
	for _, l := range lines {
		fmt.Fprintln(w, "   "+l)
	}
}

// diffHooks names every hook added, changed or removed. Unlike config, no
// filtering: almost nothing ordinary writes here, and when something does it is
// usually a real hook installer (husky, pre-commit, lefthook, git-lfs) that the
// user genuinely wants to know ran.
func diffHooks(before, after exitSnapshot) []string {
	// Neither side may be guessed at: an unwalkable hooks dir is not an empty
	// one, and saying git's own sample hooks were "removed" is a change byre
	// invented (grok).
	if !before.hooksWalked || !after.hooksWalked {
		return nil
	}
	var out []string
	for p, sig := range after.hooks {
		switch b, ok := before.hooks[p]; {
		case !ok:
			out = append(out, fmt.Sprintf("%s was added -- your git runs this, on your machine", p))
		case b != sig:
			out = append(out, fmt.Sprintf("%s changed -- your git runs this, on your machine", p))
		}
	}
	for p := range before.hooks {
		if _, ok := after.hooks[p]; !ok {
			out = append(out, fmt.Sprintf("%s was removed", p))
		}
	}
	sort.Strings(out)
	return out
}

// diffConfig names the exec-relevant git config keys that moved. Ranking keeps
// the message rare (execRelevantConfig); configValueShown and redactKeyUserinfo
// keep it from echoing anything secret-shaped, from either side of the `=`.
func diffConfig(before, after exitSnapshot) []string {
	var out []string
	// say picks the verb to match whether a value follows it. A shared
	// "is set to" for both cases left suppressed lines reading as truncated
	// English -- ".git/config: credential.helper is set to" (grok).
	say := func(file, key, value string, had bool) {
		label := redactKeyUserinfo(key)
		if configValueShown(key) && value != "" {
			verb := "is set to"
			if had {
				verb = "is now"
			}
			out = append(out, fmt.Sprintf("%s: %s %s %s", file, label, verb, value))
			return
		}
		verb := "was set"
		if had {
			verb = "changed"
		}
		out = append(out, fmt.Sprintf("%s: %s %s", file, label, verb))
	}
	gone := func(file, key, suffix string) {
		out = append(out, fmt.Sprintf("%s: %s %s", file, redactKeyUserinfo(key), suffix))
	}
	for file, akv := range after.config {
		// Unreadable on EITHER side means byre has no basis for a comparison:
		// an unreadable BEFORE would otherwise report every key as newly set
		// (codex). Say nothing rather than invent a change.
		if before.unreadable[file] || after.unreadable[file] {
			continue
		}
		// A file only one enumeration could see cannot be compared: treating a
		// newly VISIBLE worktree config as newly SET is the addition-side
		// mirror of the deletion bug (codex, grok).
		if (before.configFromListing[file] || after.configFromListing[file]) &&
			(!before.configListed || !after.configListed) {
			continue
		}
		bkv := before.config[file]
		for k, av := range akv {
			bv, had := bkv[k]
			if (had && bv == av) || !execRelevantConfig(k, av) {
				continue
			}
			say(file, k, av, had)
		}
		for k, bv := range bkv {
			if _, still := akv[k]; !still && execRelevantConfig(k, bv) {
				gone(file, k, "was removed")
			}
		}
	}
	// A watched config file that disappeared takes its keys with it -- the same
	// user-visible event as clearing them one by one. Only when it is really
	// GONE: a file byre could not read this time is still there, and saying its
	// keys vanished would be a lie byre invented (codex).
	for file, bkv := range before.config {
		if _, still := after.config[file]; still || after.unreadable[file] || before.unreadable[file] {
			continue
		}
		if before.configFromListing[file] && (!before.configListed || !after.configListed) {
			continue // never seen by both enumerations; absence proves nothing
		}
		for k, bv := range bkv {
			if execRelevantConfig(k, bv) {
				gone(file, k, "went away with the file")
			}
		}
	}
	sort.Strings(out)
	return out
}

// diffEnv names every env key that moved. Every key speaks -- naming the key
// turns a bare "this file changed" into something the reader can judge -- but
// NO VALUE is ever printed. Env files hold secrets, and a value echoed here
// would land in the terminal, in scrollback, and in any captured log: an
// exposure byre would have created. Names only, and a test pins it.
func diffEnv(before, after exitSnapshot) []string {
	// An unenumerable project root is not an empty one.
	if !before.envListed || !after.envListed {
		return nil
	}
	var out []string
	emit := func(file string, groups [3][]string) {
		for i, verb := range [3]string{"added", "changed", "removed"} {
			if len(groups[i]) == 0 {
				continue
			}
			sort.Strings(groups[i])
			out = append(out, fmt.Sprintf("%s: %s %s", file, verb, strings.Join(groups[i], ", ")))
		}
	}
	for file, akv := range after.env {
		if before.unreadable[file] || after.unreadable[file] {
			continue
		}
		bkv := before.env[file]
		var g [3][]string
		for k, av := range akv {
			switch bv, had := bkv[k]; {
			case !had:
				g[0] = append(g[0], k)
			case bv != av:
				g[1] = append(g[1], k)
			}
		}
		for k := range bkv {
			if _, still := akv[k]; !still {
				g[2] = append(g[2], k)
			}
		}
		emit(file, g)
	}
	// Deleted outright -- and only when really gone, not merely unreadable.
	for file, bkv := range before.env {
		if _, still := after.env[file]; still || after.unreadable[file] || before.unreadable[file] {
			continue
		}
		var g [3][]string
		for k := range bkv {
			g[2] = append(g[2], k)
		}
		emit(file, g)
	}
	sort.Strings(out)
	return out
}
