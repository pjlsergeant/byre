package deliver

import (
	"fmt"
	"strings"
)

// This file is the ssh-facing protocol surface of remote delivery (ADR 0037):
// the version pin, the --boxes line grammar (emit and parse), and the exit
// code that carries pool trustworthiness across the wire. Everything here is
// FROZEN once shipped — a change means a new ProtoVersion, and the handshake
// fails legibly on skew before any payload moves.

// ProtoVersion is the remote-delivery protocol this byre speaks. The number
// pins the WHOLE ssh-facing surface: the --boxes grammar, the remote flag set
// (--boxes/--tar/--box/--no-clip/--skip-uid-check), and tar-mode semantics.
const ProtoVersion = 1

// ExitPartialPool is the deliver process's exit code when --boxes printed a
// list but an engine query failed: the list is usable for picking, but
// "exactly one" is unknowable, so the caller must not auto-pick. Distinct
// from 0 (complete), 1 (failure), 2 (usage), and the transport's in-box 3.
const ExitPartialPool = 4

// CheckProto is the handshake: an invocation carrying --proto n proceeds only
// when n is the version this byre speaks. It runs before anything else — skew
// must fail before a payload, a listing, or a discovery side effect.
func CheckProto(n int) error {
	if n != ProtoVersion {
		return fmt.Errorf("remote-delivery protocol %d is not supported (this byre speaks %d) — update whichever byre is older", n, ProtoVersion)
	}
	return nil
}

// RemoteBox is one row of a --boxes listing, parsed on the local side. It is
// deliberately not a Session: there is no Engine behind it, only an id the
// remote byre will resolve itself.
type RemoteBox struct {
	ID      string
	Engine  string
	Project string
	Workdir string
	Foreign bool // listed under --skip-uid-check, owned by another uid
}

// Boxes runs discovery headlessly and emits the line grammar on cfg.Out —
// the enumerate leg of ADR 0037. Stdout is the contract (the list), stderr
// is byre's voice (notes ride it through ssh untouched), and the returned
// partial flag is the caller's cue to exit ExitPartialPool. No picking, no
// cascade: the remote never selects anything.
func Boxes(cfg Config, opts Options) (partial bool, err error) {
	p, err := discover(cfg, opts)
	if err != nil {
		return false, err
	}
	for _, s := range p.sessions {
		fmt.Fprintln(cfg.Out, boxLine(s))
	}
	if p.hidden > 0 {
		fmt.Fprintf(cfg.Err, "byre: %d %s hidden by the uid filter; --skip-uid-check to include them\n",
			p.hidden, plural(p.hidden, "session", "sessions"))
	}
	return p.partial, nil
}

// boxLine renders one session as a grammar line: five tab-separated fields —
// container id, engine, project id, workdir id, flags (comma-joined tokens;
// today only "foreign", empty when none). Fields are sanitized of control
// characters (tabs included) on emit, so the grammar is framed by the same
// rule as landed paths: one line, one row, always.
func boxLine(s Session) string {
	flags := ""
	if s.Foreign {
		flags = "foreign"
	}
	return strings.Join([]string{
		grammarField(s.ID),
		grammarField(s.EngineName),
		grammarField(s.ProjectID),
		grammarField(s.WorkdirID),
		flags,
	}, "\t")
}

// grammarField replaces control characters (tab included) with '_'; unlike
// sanitizeBase an empty field stays empty — absence is data here.
func grammarField(f string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, f)
}

// ParseBoxes parses a --boxes listing. Unparseable lines error rather than
// skip: the grammar is pinned by the handshake, so a bad line means the
// stream is not what --proto promised (or stdout got polluted), and guessing
// would deliver somewhere wrong.
func ParseBoxes(out string) ([]RemoteBox, error) {
	var boxes []RemoteBox
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			return nil, fmt.Errorf("unexpected line in the box listing (%d fields, want 5): %q", len(f), line)
		}
		if f[0] == "" {
			return nil, fmt.Errorf("unexpected line in the box listing (empty id): %q", line)
		}
		b := RemoteBox{ID: f[0], Engine: f[1], Project: f[2], Workdir: f[3]}
		for _, tok := range strings.Split(f[4], ",") {
			if tok == "foreign" {
				b.Foreign = true
			}
		}
		boxes = append(boxes, b)
	}
	return boxes, nil
}
