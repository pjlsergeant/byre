package configui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"byre/internal/config"
)

func TestParseFormatListRoundTrip(t *testing.T) {
	in := []string{"git", "jq", "curl"}
	if got := parseList(formatList(in)); !reflect.DeepEqual(got, in) {
		t.Fatalf("round-trip: got %v", got)
	}
	// blanks and whitespace are dropped
	if got := parseList("git\n\n  \njq\n"); !reflect.DeepEqual(got, []string{"git", "jq"}) {
		t.Fatalf("blank handling: got %v", got)
	}
}

func TestParseEnv(t *testing.T) {
	m, err := parseEnv("A=1\nKEY= x \n\n")
	if err != nil {
		t.Fatal(err)
	}
	if m["A"] != "1" {
		t.Fatalf("env parse: %v", m)
	}
	if m["KEY"] != " x " { // value kept EXACTLY (intentional spaces survive)
		t.Fatalf("env value should be preserved verbatim, got %q", m["KEY"])
	}
	if _, err := parseEnv("NOEQUALS"); err == nil {
		t.Error("expected error for a line without '='")
	}
	if _, err := parseEnv("=val"); err == nil {
		t.Error("expected error for an empty key")
	}
	if m, _ := parseEnv("\n  \n"); m != nil {
		t.Errorf("empty input should be nil map, got %v", m)
	}
}

func TestParseMounts(t *testing.T) {
	ms, err := parseMounts("~/data -> /data (rw)\n/etc/hosts -> /etc/hosts")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("want 2 mounts, got %v", ms)
	}
	if ms[0].Host != "~/data" || ms[0].Target != "/data" || ms[0].Mode != "rw" {
		t.Errorf("mount 0 wrong: %+v", ms[0])
	}
	if ms[1].Mode != "" { // no mode -> empty (defaults ro downstream)
		t.Errorf("mount 1 mode should be empty, got %q", ms[1].Mode)
	}
	// format then parse is stable
	if got := formatMounts(ms); !strings.Contains(got, "~/data -> /data (rw)") {
		t.Errorf("format: %s", got)
	}
	if _, err := parseMounts("no arrow here"); err == nil {
		t.Error("expected error for a line without '->'")
	}
	if _, err := parseMounts("-> /onlytarget"); err == nil {
		t.Error("expected error for a missing host")
	}
	if _, err := parseMounts("/a -> b -> /data"); err == nil {
		t.Error("expected error for an ambiguous path containing '->'")
	}
}

func TestSaveRoundTripsAndPreservesRawFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "byre.config")
	in := config.Config{
		Base:    "golang:1.22-bookworm",
		Agent:   "claude",
		Apt:     []string{"jq"},
		Mounts:  []config.Mount{{Host: "~/d", Target: "/d", Mode: "rw"}},
		RunArgs: []string{"--privileged"}, // raw field, must round-trip untouched
	}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	back, err := config.ParseFile(path)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if back.Base != in.Base || back.Agent != in.Agent {
		t.Errorf("scalars not preserved: %+v", back)
	}
	if !reflect.DeepEqual(back.RunArgs, in.RunArgs) {
		t.Errorf("raw run_args not preserved: %v", back.RunArgs)
	}
	if len(back.Mounts) != 1 || back.Mounts[0].Target != "/d" {
		t.Errorf("mounts not preserved: %v", back.Mounts)
	}
	// omitempty keeps unset fields out of the file (no noise)
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "npm_global") || strings.Contains(string(b), "files") {
		t.Errorf("unset fields should be omitted:\n%s", b)
	}
	if !strings.Contains(string(b), "Managed by `byre config`") {
		t.Errorf("missing managed-by header:\n%s", b)
	}
}
