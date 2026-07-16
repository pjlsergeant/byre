package deliver

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Remote delivery, local half (ADR 0035): `byre deliver ssh://host ...` is
// two headless ssh invocations — enumerate the remote's boxes (skipped when
// --box is already known), pick locally, then stream every source as ONE tar
// archive into a single targeted remote deliver. The remote runs its
// existing machinery; this file owns target parsing, the pack, the progress
// claim, and translating remote exit codes into legible errors.

// SSHTarget is a parsed ssh://[user@]host[:port] delivery target.
type SSHTarget struct {
	User string
	Host string
	Port string
}

// String renders the target the way a user would name it to ssh.
func (t SSHTarget) String() string {
	if t.User != "" {
		return t.User + "@" + t.Host
	}
	return t.Host
}

// ParseSSHTarget recognizes an ssh:// delivery target. isSSH reports whether
// the argument is ssh-shaped at all (a false means "treat it as a path");
// err reports an ssh-shaped argument byre cannot use.
func ParseSSHTarget(arg string) (t SSHTarget, isSSH bool, err error) {
	if !strings.HasPrefix(arg, "ssh://") {
		return SSHTarget{}, false, nil
	}
	u, err := url.Parse(arg)
	if err != nil {
		return SSHTarget{}, true, fmt.Errorf("cannot parse %q: %v", arg, err)
	}
	if u.Hostname() == "" {
		return SSHTarget{}, true, fmt.Errorf("%q names no host", arg)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return SSHTarget{}, true, fmt.Errorf("%q carries more than [user@]host[:port] — the ssh target names a machine, sources follow as arguments", arg)
	}
	t = SSHTarget{Host: u.Hostname(), Port: u.Port()}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			return SSHTarget{}, true, fmt.Errorf("%q embeds a password — ssh owns authentication (keys, agents, prompts)", arg)
		}
		t.User = u.User.Username()
	}
	return t, true, nil
}

// SSHExec runs one remote command. remoteArgv is the command for the remote
// shell (the implementation owns quoting and ssh's own flags); a remote exit
// status comes back as *SSHExitError, transport failures as themselves.
type SSHExec func(t SSHTarget, remoteArgv []string, stdin io.Reader, stdout, stderr io.Writer) error

// SSHExitError is a remote command's nonzero exit status. ssh reserves 255
// for its own failures; anything else is the remote command's.
type SSHExitError struct{ Code int }

func (e *SSHExitError) Error() string { return fmt.Sprintf("remote exit status %d", e.Code) }

// RunRemote delivers sources to a box on an ssh remote and returns the
// landed in-box paths the remote reported. showProgress enables the sending
// meter (the caller's TTY knowledge).
func RunRemote(cfg Config, opts Options, target SSHTarget, sources []Source, sshExec SSHExec, showProgress bool) ([]string, error) {
	remoteByre := opts.RemoteByre
	if remoteByre == "" {
		remoteByre = "byre"
	}

	boxID := opts.Box
	if boxID == "" {
		id, err := pickRemoteBox(cfg, opts, target, remoteByre, sshExec)
		if err != nil {
			return nil, err
		}
		boxID = id
	}

	plan, cleanup, err := planPack(cfg.Err, sources)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	if len(plan.entries) == 0 {
		return nil, fmt.Errorf("nothing deliverable (every source was skipped — see the notes above)")
	}

	argv := []string{remoteByre, "deliver", "--proto", strconv.Itoa(ProtoVersion), "--box", boxID, "--no-clip"}
	if opts.SkipUIDCheck {
		argv = append(argv, "--skip-uid-check")
	}
	argv = append(argv, "--tar", "-")

	meter := &sendMeter{err: cfg.Err, total: plan.bytes, enabled: showProgress}
	pr, pw := io.Pipe()
	packDone := make(chan error, 1)
	go func() {
		err := plan.writeTo(pw, meter)
		pw.CloseWithError(err)
		packDone <- err
	}()
	var remoteOut bytes.Buffer
	// Remote stderr flows through the meter's guard: a note arriving
	// mid-progress-line clears the line first (both write cfg.Err, from
	// different goroutines — the meter's lock serializes them).
	sshErr := sshExec(target, argv, pr, &remoteOut, meterGuard{m: meter, w: cfg.Err})
	pr.Close() // unblocks the packer if ssh died mid-stream
	packErr := <-packDone
	meter.finish(sshErr == nil && packErr == nil)

	// The paths that landed are real whatever else failed: print them, ship
	// them to the clipboard, and only then report the failure.
	landed := parseLandedPaths(remoteOut.String())
	for _, p := range landed {
		fmt.Fprintln(cfg.Out, p)
	}
	shipClipboard(cfg, opts, landed)

	if packErr != nil && !errors.Is(packErr, io.ErrClosedPipe) {
		// A local read failed mid-pack: the archive was cut short and the
		// remote's own complaint is about the symptom — report the cause.
		return landed, fmt.Errorf("packing the delivery: %w", packErr)
	}
	if sshErr != nil {
		return landed, remoteFailure(sshErr, target, remoteByre, "delivery")
	}
	return landed, nil
}

// pickRemoteBox runs the enumeration leg and resolves it to one box id:
// remote --boxes, then the local three-way branch — zero errors, a sole box
// on a COMPLETE pool wins, anything else is the picker's moment (or its
// no-picker listing degradation). Not a cascade: there is no remote cwd and
// the uid filter already ran remotely.
func pickRemoteBox(cfg Config, opts Options, target SSHTarget, remoteByre string, sshExec SSHExec) (string, error) {
	argv := []string{remoteByre, "deliver", "--boxes", "--proto", strconv.Itoa(ProtoVersion)}
	if opts.SkipUIDCheck {
		argv = append(argv, "--skip-uid-check")
	}
	var out bytes.Buffer
	partial := false
	if err := sshExec(target, argv, strings.NewReader(""), &out, cfg.Err); err != nil {
		var xe *SSHExitError
		if errors.As(err, &xe) && xe.Code == ExitPartialPool {
			// The list printed and stays usable; "exactly one" is unknowable.
			partial = true
		} else {
			return "", remoteFailure(err, target, remoteByre, "listing boxes")
		}
	}
	boxes, err := ParseBoxes(out.String())
	if err != nil {
		return "", fmt.Errorf("listing boxes on %s: %w", target, err)
	}
	if len(boxes) == 0 {
		if partial {
			return "", fmt.Errorf("no boxes visible on %s (and an engine query failed there — see the notes above)", target)
		}
		return "", fmt.Errorf("no running byre boxes on %s (notes from the remote, if any, are above)", target)
	}
	sessions := make([]Session, len(boxes))
	for i, b := range boxes {
		sessions[i] = Session{ID: b.ID, EngineName: b.Engine, ProjectID: b.Project, WorkdirID: b.Workdir, Foreign: b.Foreign}
	}
	if len(sessions) == 1 && !partial {
		return sessions[0].ID, nil
	}
	if partial {
		fmt.Fprintf(cfg.Err, "byre: an engine query failed on %s — pick explicitly\n", target)
	}
	if cfg.Pick != nil {
		s, ok, err := cfg.Pick(sessions)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errCancelled
		}
		return s.ID, nil
	}
	return "", fmt.Errorf("%d %s running on %s — pick one with --box:\n%s",
		len(sessions), plural(len(sessions), "box is", "boxes are"), target, sessionList(sessions))
}

// remoteFailure translates a failed remote invocation into the error a human
// can act on. The remote's own stderr already streamed through, so this
// names the cause, not the transcript.
func remoteFailure(err error, target SSHTarget, remoteByre, doing string) error {
	var xe *SSHExitError
	if !errors.As(err, &xe) {
		return fmt.Errorf("%s on %s: %w", doing, target, err)
	}
	switch xe.Code {
	case 127:
		return fmt.Errorf("%s: no %q on %s's ssh PATH — sshd's non-interactive PATH is sparse (stock macOS omits /usr/local/bin); point --remote-byre at the binary", doing, remoteByre, target)
	case 126:
		return fmt.Errorf("%s: %q on %s exists but isn't executable", doing, remoteByre, target)
	case 2:
		return fmt.Errorf("%s: byre on %s is too old for remote delivery (it doesn't speak --proto %d) — update it", doing, target, ProtoVersion)
	case 255:
		return fmt.Errorf("ssh to %s failed — its message is above", target)
	default:
		return fmt.Errorf("%s on %s failed — the remote's message is above", doing, target)
	}
}

// parseLandedPaths reads the remote deliver's stdout: landed in-box paths,
// one per line (the same contract a local deliver prints).
func parseLandedPaths(out string) []string {
	var landed []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			landed = append(landed, line)
		}
	}
	return landed
}

// packPlan is a delivery's tar layout, statted and spooled up front so the
// send meter has a truthful total before the first byte moves.
type packPlan struct {
	entries []packEntry
	bytes   int64 // content bytes (headers excluded — the meter's total)
}

// packEntry is one archive member. Exactly one of path/data backs a file
// entry; dir entries have neither.
type packEntry struct {
	name string // slash-form archive name
	path string // host file to stream
	data []byte // in-memory content (clipboard captures)
	size int64
	dir  bool
}

// planPack walks the sources into a pack plan, mirroring local delivery's
// rules: directories recurse with structure kept, file symlinks follow,
// directory symlinks and non-regular files skip with a note. Reader sources
// (stdin) spool to a temp file to learn their size — tar headers need it
// before content. cleanup removes the spools (non-nil even on error).
func planPack(warn io.Writer, sources []Source) (plan *packPlan, cleanup func(), err error) {
	plan = &packPlan{}
	var spools []string
	cleanup = func() {
		for _, s := range spools {
			os.Remove(s)
		}
	}
	seen := map[string]bool{} // top-level archive names; collisions uniquify locally for legibility
	claim := func(name string) string {
		stem, ext, _ := splitName(name)
		n := stem + ext
		for k := 2; seen[n]; k++ {
			n = fmt.Sprintf("%s-%d%s", stem, k, ext)
		}
		seen[n] = true
		return n
	}
	for _, src := range sources {
		switch {
		case src.Path != "":
			if err := planPath(warn, plan, claim, src.Path); err != nil {
				return plan, cleanup, err
			}
		case src.Data != nil:
			plan.entries = append(plan.entries, packEntry{name: claim(src.Name), data: src.Data, size: int64(len(src.Data))})
			plan.bytes += int64(len(src.Data))
		case src.Reader != nil:
			f, err := os.CreateTemp("", "byre-deliver-spool-*")
			if err != nil {
				return plan, cleanup, fmt.Errorf("spooling %s: %w", src.label(), err)
			}
			spools = append(spools, f.Name())
			n, err := io.Copy(f, src.Reader)
			f.Close()
			if err != nil {
				return plan, cleanup, fmt.Errorf("spooling %s: %w", src.label(), err)
			}
			plan.entries = append(plan.entries, packEntry{name: claim(src.Name), path: f.Name(), size: n})
			plan.bytes += n
		default:
			return plan, cleanup, fmt.Errorf("empty source %q", src.Name)
		}
	}
	return plan, cleanup, nil
}

// planPath plans one path argument: a file entry, or a directory subtree.
func planPath(warn io.Writer, plan *packPlan, claim func(string) string, src string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("delivering %s: %w", src, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("delivering %s: broken symlink: %w", src, err)
		}
		if target.IsDir() {
			fmt.Fprintf(warn, "byre: skipping %s (symlink to a directory)\n", src)
			return nil
		}
		info = target
	}
	switch {
	case info.Mode().IsRegular():
		plan.entries = append(plan.entries, packEntry{name: claim(filepath.Base(src)), path: src, size: info.Size()})
		plan.bytes += info.Size()
		return nil
	case info.IsDir():
		root := claim(filepath.Base(src))
		plan.entries = append(plan.entries, packEntry{name: root + "/", dir: true})
		return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("delivering %s: %w", p, err)
			}
			if p == src {
				return nil
			}
			rel, rerr := filepath.Rel(src, p)
			if rerr != nil {
				return rerr
			}
			name := root + "/" + filepath.ToSlash(rel)
			switch {
			case d.Type()&os.ModeSymlink != 0:
				st, serr := os.Stat(p)
				if serr != nil || st.IsDir() {
					fmt.Fprintf(warn, "byre: skipping %s (symlink to a directory, or broken)\n", p)
					return nil
				}
				plan.entries = append(plan.entries, packEntry{name: name, path: p, size: st.Size()})
				plan.bytes += st.Size()
			case d.IsDir():
				plan.entries = append(plan.entries, packEntry{name: name + "/", dir: true})
			case d.Type().IsRegular():
				st, serr := d.Info()
				if serr != nil {
					return fmt.Errorf("delivering %s: %w", p, serr)
				}
				plan.entries = append(plan.entries, packEntry{name: name, path: p, size: st.Size()})
				plan.bytes += st.Size()
			default:
				fmt.Fprintf(warn, "byre: skipping %s (not a regular file or directory)\n", p)
			}
			return nil
		})
	default:
		fmt.Fprintf(warn, "byre: skipping %s (not a regular file or directory)\n", src)
		return nil
	}
}

// writeTo streams the plan as a tar archive, feeding content bytes through
// the meter. A file that changed size since planning aborts the pack — the
// header already promised a length, and a silently padded or clipped file
// would be a corrupt delivery.
func (p *packPlan) writeTo(w io.Writer, m *sendMeter) error {
	tw := tar.NewWriter(w)
	for _, e := range p.entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: e.size}
		if e.dir {
			hdr.Typeflag = tar.TypeDir
			hdr.Mode = 0o755
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if e.dir {
			continue
		}
		var content io.Reader
		if e.data != nil {
			content = bytes.NewReader(e.data)
		} else {
			f, err := os.Open(e.path)
			if err != nil {
				return err
			}
			content = f
		}
		n, err := io.Copy(tw, io.TeeReader(io.LimitReader(content, e.size), m))
		if c, ok := content.(io.Closer); ok {
			c.Close()
		}
		if err != nil {
			return err
		}
		if n != e.size {
			return fmt.Errorf("%s changed while being sent (%d of %d bytes)", e.name, n, e.size)
		}
	}
	return tw.Close()
}

// sendMeter is the sending progress claim: a single stderr line, redrawn in
// place, byte-honest about what it measures (bytes handed to ssh, not bytes
// landed — the label says "sending", never "delivered"). Silent when not
// enabled (no TTY) or when the whole delivery is small enough to be instant.
// The pack goroutine feeds it while ssh's stderr passthrough interrupts it;
// the lock serializes both against one terminal.
type sendMeter struct {
	mu      sync.Mutex
	err     io.Writer
	total   int64
	n       int64
	last    int64
	enabled bool
	drawn   bool // a progress line currently occupies the terminal line
	drew    bool // ever drew (finish resolves only a meter that appeared)
}

// meterStep is how many bytes pass between redraws; small enough to feel
// live, large enough to cost nothing.
const meterStep = 256 * 1024

func (m *sendMeter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n += int64(len(p))
	if m.enabled && m.total > meterStep && (m.n-m.last >= meterStep || m.n == m.total) {
		m.last = m.n
		pct := int64(100)
		if m.total > 0 {
			pct = m.n * 100 / m.total
		}
		fmt.Fprintf(m.err, "\rbyre: sending %s / %s (%d%%)", sizeString(m.n), sizeString(m.total), pct)
		m.drawn, m.drew = true, true
	}
	return len(p), nil
}

// finish resolves the meter line: a sent-total on success (the remote's own
// summary already streamed through the guard), a clean break on failure.
func (m *sendMeter) finish(ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.drew {
		return
	}
	if ok {
		fmt.Fprintf(m.err, "\rbyre: sent %s\x1b[K\n", sizeString(m.total))
		return
	}
	if m.drawn {
		fmt.Fprintln(m.err)
	}
}

// meterGuard forwards remote stderr through the meter's lock, clearing a
// drawn progress line first so notes land on their own lines (the meter
// redraws on its next step).
type meterGuard struct {
	m *sendMeter
	w io.Writer
}

func (g meterGuard) Write(p []byte) (int, error) {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	if g.m.drawn {
		fmt.Fprint(g.w, "\r\x1b[K")
		g.m.drawn = false
	}
	return g.w.Write(p)
}
