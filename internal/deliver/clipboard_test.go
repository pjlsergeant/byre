package deliver

import (
	"fmt"
	"strings"
	"testing"
)

func TestQuoteIfNeeded(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/inbox/report.pdf", "/inbox/report.pdf"},
		{"/inbox/a_b-c.1.txt", "/inbox/a_b-c.1.txt"},
		{"/inbox/Screenshot 2026-07-10 at 3.14.15 PM.png", "'/inbox/Screenshot 2026-07-10 at 3.14.15 PM.png'"},
		{"/inbox/it's.txt", `'/inbox/it'\''s.txt'`},
		{"/inbox/a$b", "'/inbox/a$b'"},
	}
	for _, c := range cases {
		if got := quoteIfNeeded(c.in); got != c.want {
			t.Errorf("quoteIfNeeded(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClipboardPayloadOnePerLine(t *testing.T) {
	got := clipboardPayload([]string{"/inbox/a.png", "/inbox/b c.png"})
	want := "/inbox/a.png\n'/inbox/b c.png'"
	if got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestClipboardRoundTrip(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	var wrote string
	cfg.Clip = &Clipboard{Name: "pbcopy", Write: func(s string) error { wrote = s; return nil }}
	src := writeFile(t, "shot.png", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if wrote != "/inbox/shot.png" {
		t.Fatalf("clipboard = %q", wrote)
	}
	if !strings.Contains(errw.String(), "path copied to the clipboard (pbcopy)") {
		t.Fatalf("no feedback line: %q", errw.String())
	}
}

func TestClipboardUnavailableSaysSo(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw.String(), "clipboard unavailable — path printed above") {
		t.Fatalf("no degrade claim: %q", errw.String())
	}
}

func TestClipboardWriteFailureDegrades(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, errw := testConfig(eng)
	cfg.Clip = &Clipboard{Name: "xclip", Write: func(string) error { return fmt.Errorf("no display") }}
	src := writeFile(t, "f.txt", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err) // a failed clipboard write must NOT fail the delivery
	}
	if !strings.Contains(errw.String(), "clipboard write failed") {
		t.Fatalf("no degrade claim: %q", errw.String())
	}
	if !strings.Contains(out.String(), "/inbox/f.txt") {
		t.Fatalf("stdout must still carry the path: %q", out.String())
	}
}

func TestNoClipSkipsSilently(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	called := false
	cfg.Clip = &Clipboard{Name: "pbcopy", Write: func(string) error { called = true; return nil }}
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{NoClip: true}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("clipboard written despite --no-clip")
	}
	if strings.Contains(errw.String(), "clipboard") {
		t.Fatalf("--no-clip should be silent about the clipboard: %q", errw.String())
	}
}

func TestBestEffortClaimIsHedged(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	cfg.Clip = &Clipboard{Name: "OSC 52", BestEffort: true, Write: func(string) error { return nil }}
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw.String(), "OSC 52 (best-effort)") {
		t.Fatalf("best-effort claim not hedged: %q", errw.String())
	}
}
