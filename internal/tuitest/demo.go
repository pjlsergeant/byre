package tuitest

// Demo recording: the publish-time asciinema pipeline riding the same tmux
// harness (the fourth consumer of the substrate — design:
// docs/marketing/positioning.md "Publish-time asciinema demos"). A demo test
// is a normal scenario (send keys, WaitFor) with an asciinema spectator
// attached to the private tmux session; the WaitFors are the assertions that
// make a broken layout fail the publish, and the cast is the artifact.
//
// The pipeline's one hard-won discipline (prototyped 2026-07-17): ending a
// recording by killing the tmux server leaves "[server exited]" as the final
// frame, which breaks the poster-frame rule (P11: poster = the intended final
// screen). EndCast trims trailing events back to the last output event
// containing a sentinel the scenario knows is painted on that screen.
//
// Multi-scene demos record each scene as its own session/cast (each ending
// clean at its sentinel) and concatenate with a scene break — so a mid-demo
// process end (e.g. develop stopping at the engine boundary) never shows; the
// cut is a visible scene change, not an edited-out frame.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// RequireDemo gates a demo-recording test: skip without BYRE_DEMO_REC=1, fail
// loudly when the gate is set but a tool is missing (same stance as Require —
// in CI a silent skip would publish a site with holes where casts should be).
func RequireDemo(t *testing.T) {
	t.Helper()
	if os.Getenv("BYRE_DEMO_REC") != "1" {
		t.Skip("set BYRE_DEMO_REC=1 to record site demos (needs tmux + asciinema)")
	}
	for _, tool := range []string{"tmux", "asciinema"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("BYRE_DEMO_REC=1 but no %s on PATH — the gate set without the tool is a configuration error, not a skip", tool)
		}
	}
}

// startRecorder attaches the asciinema spectator to the session: a headless
// `asciinema rec` whose recorded command is a tmux client attached to the
// pane, geometry pinned so the cast can never disagree with the pane. Called
// by Start between session creation and the real argv's respawn — the
// spectator must be watching before the process can paint its first frame.
func (s *Session) startRecorder(path string, cols, rows int) {
	s.t.Helper()
	cmd := exec.Command("asciinema", "rec", "--headless", "--overwrite",
		// The idle cap is header metadata the player honors at playback, so a
		// slow WaitFor never becomes a dead gap in the published demo.
		"--idle-time-limit", "2",
		// Capture input events too: what reaches the spectator's stdin is
		// recorded as "i" events (the trim already matches sentinels against
		// output events only).
		"--capture-input",
		"--window-size", fmt.Sprintf("%dx%d", cols, rows),
		"-c", fmt.Sprintf("tmux -L %s attach -t main", s.socket),
		path)
	// The spectator's tmux client needs a sane TERM regardless of the CI
	// runner's — and an explicit UTF-8 locale: tmux renders to each CLIENT
	// per that client's locale, and with none set it substitutes every
	// non-ASCII glyph with "_" in the recorded stream (found live: em-dashes
	// as underscores in published casts, while capture-pane — the server
	// grid — showed them fine, so the assertion tier never noticed).
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LC_ALL=C.UTF-8")
	if err := cmd.Start(); err != nil {
		s.t.Fatalf("starting asciinema: %v", err)
	}
	s.rec = cmd
	s.castPath = path
	// A scenario that fails before EndCast must not leave the recorder
	// running; registered after Start's kill-server cleanup, so this runs
	// first (LIFO), then the server teardown ends the spectator's attach.
	s.t.Cleanup(func() {
		if s.rec != nil {
			_ = s.rec.Process.Kill()
			_ = s.rec.Wait()
		}
	})
	// The pane repaints for the attaching client; wait until tmux reports the
	// spectator before letting the real process near the pty.
	deadline := time.Now().Add(waitDefault)
	for {
		if strings.TrimSpace(s.tmux("list-clients")) != "" {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatal("timeout waiting for the asciinema spectator to attach")
		}
		time.Sleep(pollEvery)
	}
}

// TypeHuman sends literal text one rune at a time with a human cadence, so a
// recorded demo shows typing rather than text appearing at once. Outside a
// recording it is just a slow Type.
func (s *Session) TypeHuman(text string) {
	s.t.Helper()
	for _, r := range text {
		s.tmux("send-keys", "-t", "main", "-l", string(r))
		time.Sleep(70 * time.Millisecond)
	}
}

// EndCast stops the recording and trims its tail: kills the tmux server (the
// attached spectator exits with it, ending the rec), then drops every event
// after the last output event containing sentinel — a string the scenario
// knows is painted on the intended final screen. Returns the cast path.
func (s *Session) EndCast(sentinel string) string {
	s.t.Helper()
	if s.rec == nil {
		s.t.Fatal("EndCast on a session that isn't recording (set Opts.RecordTo)")
	}
	_ = exec.Command("tmux", "-L", s.socket, "kill-server").Run()
	done := make(chan error, 1)
	go func() { done <- s.rec.Wait() }()
	select {
	case <-done:
		// The recorder's own exit status is uninteresting: the cast on disk is
		// what gets validated next.
	case <-time.After(waitDefault):
		_ = s.rec.Process.Kill()
		s.t.Fatal("timeout waiting for asciinema to finish writing the cast")
	}
	s.rec = nil
	raw, err := os.ReadFile(s.castPath)
	if err != nil {
		s.t.Fatalf("reading the recorded cast: %v", err)
	}
	trimmed, err := trimCastTail(string(raw), sentinel)
	if err != nil {
		s.t.Fatalf("trimming %s: %v", s.castPath, err)
	}
	// A no-op output event pads the tail so the final screen lingers in
	// playback instead of the cast ending the instant it paints.
	header, events, err := parseCast(trimmed)
	if err != nil {
		s.t.Fatal(err)
	}
	events = append(events, castEvent{2, "o", ""})
	if err := os.WriteFile(s.castPath, []byte(renderCast(header, events)), 0o644); err != nil {
		s.t.Fatal(err)
	}
	return s.castPath
}

// A cast (asciicast v3) is JSON lines: a header object, then
// [interval, code, data] events — intervals, not timestamps, which is what
// makes tail-trimming and concatenation safe: dropping or appending events
// never shifts the timing of the ones kept.
type castEvent struct {
	interval float64
	code     string
	data     string
}

func parseCast(raw string) (header string, events []castEvent, err error) {
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "{") {
		return "", nil, fmt.Errorf("not an asciicast: no header line")
	}
	header = lines[0]
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e []any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return "", nil, fmt.Errorf("event line %d: %w", i+2, err)
		}
		if len(e) != 3 {
			return "", nil, fmt.Errorf("event line %d: %d fields, want 3", i+2, len(e))
		}
		interval, ok1 := e[0].(float64)
		code, ok2 := e[1].(string)
		data, ok3 := e[2].(string)
		if !ok1 || !ok2 || !ok3 {
			return "", nil, fmt.Errorf("event line %d: unexpected field types", i+2)
		}
		events = append(events, castEvent{interval, code, data})
	}
	return header, events, nil
}

func renderCast(header string, events []castEvent) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	for _, e := range events {
		line, _ := json.Marshal([]any{e.interval, e.code, e.data})
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// trimCastTail cuts the cast at the sentinel's FIRST paint: every event
// after the first output event containing sentinel is dropped, and that
// event itself is truncated at the sentinel's own line ending (a pty read
// can coalesce the sentinel's line with whatever printed next — found live:
// the engine-boundary error riding in the same event as the config-written
// line). First, not last: after the intended final frame the terminal may
// keep moving (the error line, then a full-screen tmux repaint that paints
// the sentinel AGAIN above the error — found live too), so any later
// occurrence is exactly the footage the trim exists to drop. The authoring
// rule that makes this correct: pick a sentinel whose first appearance IS
// the intended final frame, painted in one write (styling can split a
// string across events). A miss is a loud error naming the fix, not a cast
// that ships with the server-exited frame as its poster.
func trimCastTail(raw, sentinel string) (string, error) {
	header, events, err := parseCast(raw)
	if err != nil {
		return "", err
	}
	for i, e := range events {
		if e.code != "o" || !strings.Contains(e.data, sentinel) {
			continue
		}
		kept := append([]castEvent{}, events[:i+1]...)
		end := strings.Index(e.data, sentinel) + len(sentinel)
		if nl := strings.Index(e.data[end:], "\n"); nl >= 0 {
			kept[i].data = e.data[:end+nl+1]
		}
		return renderCast(header, kept), nil
	}
	return "", fmt.Errorf("sentinel %q appears in no output event — pick a string the final screen paints in one run (styling splits text across events)", sentinel)
}

// sceneBreak is what the viewer sees between concatenated scenes: a reset and
// a clear, so the next scene paints on a blank screen however the previous
// one ended.
const sceneBreak = "\x1b[0m\x1b[2J\x1b[3J\x1b[H"

// concatCasts joins already-trimmed scene casts into one: the first scene's
// header carries the geometry (scenes must agree), and each later scene is
// prefixed by a clear-screen event holding the screen for pause seconds.
func concatCasts(pause float64, scenes ...string) (string, error) {
	if len(scenes) == 0 {
		return "", fmt.Errorf("no scenes")
	}
	header, events, err := parseCast(scenes[0])
	if err != nil {
		return "", fmt.Errorf("scene 1: %w", err)
	}
	cols, rows, err := castGeometry(header)
	if err != nil {
		return "", fmt.Errorf("scene 1: %w", err)
	}
	for i, scene := range scenes[1:] {
		h, evs, err := parseCast(scene)
		if err != nil {
			return "", fmt.Errorf("scene %d: %w", i+2, err)
		}
		c, r, err := castGeometry(h)
		if err != nil {
			return "", fmt.Errorf("scene %d: %w", i+2, err)
		}
		if c != cols || r != rows {
			return "", fmt.Errorf("scene %d is %dx%d, scene 1 is %dx%d — scenes must share geometry", i+2, c, r, cols, rows)
		}
		events = append(events, castEvent{pause, "o", sceneBreak})
		events = append(events, evs...)
	}
	return renderCast(header, events), nil
}

// castHeader is the slice of the v3 header the harness reads back.
type castHeader struct {
	Term struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"term"`
}

func castGeometry(header string) (cols, rows int, err error) {
	var h castHeader
	if err := json.Unmarshal([]byte(header), &h); err != nil {
		return 0, 0, fmt.Errorf("parsing cast header: %w", err)
	}
	if h.Term.Cols == 0 || h.Term.Rows == 0 {
		return 0, 0, fmt.Errorf("cast header carries no geometry")
	}
	return h.Term.Cols, h.Term.Rows, nil
}

// sanitizeHeader strips the recorder's session metadata (the harness's tmux
// attach command, the recording env) from a published cast, keeping only
// what a player consumes: version, geometry, and the idle cap.
func sanitizeHeader(header string) (string, error) {
	var h map[string]any
	if err := json.Unmarshal([]byte(header), &h); err != nil {
		return "", fmt.Errorf("parsing cast header: %w", err)
	}
	kept := map[string]any{}
	for _, k := range []string{"version", "term", "idle_time_limit"} {
		if v, ok := h[k]; ok {
			kept[k] = v
		}
	}
	out, err := json.Marshal(kept)
	return string(out), err
}

// castDuration sums event intervals — the demo's playing time, which the
// site's player uses to poster the final frame (P11).
func castDuration(events []castEvent) float64 {
	var d float64
	for _, e := range events {
		d += e.interval
	}
	return d
}

// assembleDemo joins scene casts and produces the publishable artifact pair:
// the cast (scene-concatenated, header sanitized of recorder metadata) and
// its metadata JSON (duration and geometry for the player shortcode; poster
// = final frame needs the duration, and the cast itself is JSON lines Hugo
// can't read). Pure — WriteDemo owns the file I/O around it.
func assembleDemo(raws []string) (cast, meta string, err error) {
	joined, err := concatCasts(1.2, raws...)
	if err != nil {
		return "", "", err
	}
	header, events, err := parseCast(joined)
	if err != nil {
		return "", "", err
	}
	header, err = sanitizeHeader(header)
	if err != nil {
		return "", "", err
	}
	cols, rows, err := castGeometry(header)
	if err != nil {
		return "", "", err
	}
	m, err := json.Marshal(map[string]any{
		"duration": castDuration(events),
		"cols":     cols,
		"rows":     rows,
	})
	if err != nil {
		return "", "", err
	}
	return renderCast(header, events), string(m) + "\n", nil
}

// WriteDemo assembles a demo from its scene casts and installs it where the
// site build picks it up: site/static/casts/<slug>.cast + <slug>.json.
func WriteDemo(t *testing.T, slug string, scenes ...string) {
	t.Helper()
	raws := make([]string, len(scenes))
	for i, p := range scenes {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		raws[i] = string(b)
	}
	cast, meta, err := assembleDemo(raws)
	if err != nil {
		t.Fatalf("assembling demo %s: %v", slug, err)
	}
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "site", "static", "casts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".cast"), []byte(cast), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	_, events, _ := parseCast(cast)
	t.Logf("demo %s: %d events, %.1fs", slug, len(events), castDuration(events))
}
