package deliver

import (
	"fmt"
	"strings"
	"testing"
)

func TestCheckProto(t *testing.T) {
	if err := CheckProto(ProtoVersion); err != nil {
		t.Fatalf("same version: %v", err)
	}
	err := CheckProto(ProtoVersion + 1)
	if err == nil {
		t.Fatal("future version accepted")
	}
	for _, want := range []string{fmt.Sprint(ProtoVersion + 1), fmt.Sprint(ProtoVersion), "update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("mismatch error %q lacks %q", err, want)
		}
	}
}

func TestBoxesGrammar(t *testing.T) {
	docker := box("docker", "aaa", "bbb")
	podman := box("podman", "ccc")
	podman.callerScoped = true
	cfg, out, errw := testConfig(docker, podman)
	partial, err := Boxes(cfg, Options{})
	if err != nil || partial {
		t.Fatalf("Boxes = partial %v, err %v", partial, err)
	}
	want := "aaa\tdocker\tproj-aaa\tproj-aaa\t\n" +
		"bbb\tdocker\tproj-bbb\tproj-bbb\t\n" +
		"ccc\tpodman\tproj-ccc\tproj-ccc\t\n"
	if out.String() != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", out.String(), want)
	}
	if errw.String() != "" {
		t.Fatalf("unexpected stderr: %q", errw.String())
	}
}

func TestBoxesForeignAndHidden(t *testing.T) {
	eng := box("docker", "aaa", "bbb")
	eng.env["bbb"] = map[string]string{"BYRE_UID": "777", "BYRE_GID": "20"}

	// Hidden by default: the foreign box is a count on stderr, not a row.
	cfg, out, errw := testConfig(eng)
	if _, err := Boxes(cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "bbb") {
		t.Fatalf("foreign box listed without --skip-uid-check: %q", out.String())
	}
	if !strings.Contains(errw.String(), "1 session hidden by the uid filter") {
		t.Fatalf("no hidden note: %q", errw.String())
	}

	// Revealed and flagged under --skip-uid-check.
	cfg2, out2, _ := testConfig(eng)
	if _, err := Boxes(cfg2, Options{SkipUIDCheck: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), "bbb\tdocker\tproj-bbb\tproj-bbb\tforeign\n") {
		t.Fatalf("foreign row missing its flag: %q", out2.String())
	}
}

func TestBoxesPartialPool(t *testing.T) {
	broken := box("docker", "aaa")
	broken.idsErr = fmt.Errorf("500 server error")
	ok := box("podman", "ccc")
	cfg, out, errw := testConfig(broken, ok)
	partial, err := Boxes(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !partial {
		t.Fatal("failed engine query did not mark the pool partial")
	}
	if !strings.Contains(out.String(), "ccc\tpodman") {
		t.Fatalf("healthy engine's box missing: %q", out.String())
	}
	if !strings.Contains(errw.String(), "docker query failed") {
		t.Fatalf("no loud degradation: %q", errw.String())
	}
}

func TestBoxesUnreachableEngineIsComplete(t *testing.T) {
	gone := box("podman", "x")
	gone.idsErr = fmt.Errorf("Cannot connect to Podman")
	ok := box("docker", "aaa")
	cfg, _, _ := testConfig(ok, gone)
	partial, err := Boxes(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if partial {
		t.Fatal("an unreachable engine must count as answered-with-zero, not partial")
	}
}

func TestBoxLineSanitizesFields(t *testing.T) {
	line := boxLine(Session{ID: "id1", EngineName: "docker", ProjectID: "has\ttab", WorkdirID: "has\nnewline"})
	if line != "id1\tdocker\thas_tab\thas_newline\t" {
		t.Fatalf("line = %q", line)
	}
}

func TestParseBoxes(t *testing.T) {
	boxes, err := ParseBoxes("aaa\tdocker\tproj\twd\t\nbbb\tpodman\tp2\t\tforeign\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 2 {
		t.Fatalf("boxes = %+v", boxes)
	}
	if boxes[0] != (RemoteBox{ID: "aaa", Engine: "docker", Project: "proj", Workdir: "wd"}) {
		t.Fatalf("row 0 = %+v", boxes[0])
	}
	if boxes[1] != (RemoteBox{ID: "bbb", Engine: "podman", Project: "p2", Foreign: true}) {
		t.Fatalf("row 1 = %+v", boxes[1])
	}
}

func TestParseBoxesRoundTrip(t *testing.T) {
	s := Session{ID: "deadbeef", EngineName: "docker", ProjectID: "proj", WorkdirID: "proj-wt", Foreign: true}
	boxes, err := ParseBoxes(boxLine(s) + "\n")
	if err != nil {
		t.Fatal(err)
	}
	want := RemoteBox{ID: "deadbeef", Engine: "docker", Project: "proj", Workdir: "proj-wt", Foreign: true}
	if len(boxes) != 1 || boxes[0] != want {
		t.Fatalf("round trip = %+v, want %+v", boxes, want)
	}
}

func TestParseBoxesRejectsPollution(t *testing.T) {
	for _, in := range []string{
		"aaa\tdocker\tproj\n",               // too few fields
		"aaa\tdocker\tproj\twd\tf\textra\n", // too many
		"\tdocker\tproj\twd\t\n",            // empty id
		"some banner text\n",                // stdout pollution
	} {
		if _, err := ParseBoxes(in); err == nil {
			t.Errorf("ParseBoxes(%q) accepted", in)
		}
	}
}

func TestParseBoxesEmpty(t *testing.T) {
	boxes, err := ParseBoxes("")
	if err != nil || boxes != nil {
		t.Fatalf("empty listing = %v, %v", boxes, err)
	}
}
