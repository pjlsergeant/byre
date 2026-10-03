package runner

import (
	"strings"
	"testing"
)

// The helper argv is a CONTRACT: every flag on it is a property the backup and
// restore verbs rest on, and a flag that quietly disappears would not fail any
// behaviour test -- the capture would simply read the wrong bytes, or reach the
// network, or populate the volume from the image. Pinned byte-exact for that
// reason, with each clause's reason named in the design (ADR 0006: the list is
// closed and byre's own).
func TestHelperArgvCapture(t *testing.T) {
	h := Helper{
		Image:     "byre-proj-u1000-g1000",
		Identity:  Identity{UID: 1000, GID: 1000},
		Labels:    []string{"byre.helper=proj-abc123", "byre.helper.run=deadbeefdeadbeef"},
		Env:       map[string]string{"TAR_OPTIONS": ""},
		Volume:    "byre-proj-abc123-.claude",
		MountPath: "/.byre-helper-deadbeefdeadbeef",
		ReadOnly:  true,
		Script:    "tar --format=posix --numeric-owner -c -f - -C /.byre-helper-deadbeefdeadbeef .",
	}
	want := "run --rm -i --entrypoint sh -u 0:0 --network none" +
		" --label byre.helper=proj-abc123 --label byre.helper.run=deadbeefdeadbeef" +
		" -e TAR_OPTIONS=" +
		" -v byre-proj-abc123-.claude:/.byre-helper-deadbeefdeadbeef:ro,nocopy" +
		" byre-proj-u1000-g1000 -c " + h.Script
	args := helperArgs(Docker, h)
	if got := strings.Join(args, " "); got != want {
		t.Fatalf("capture argv =\n%s\nwant\n%s", got, want)
	}
	// No host bind of any kind, and no engine flag that could reintroduce one.
	// The volume rides a `-v`, so what makes it a named volume rather than a
	// bind is its source: a path there would mount the host.
	for i, arg := range args {
		if strings.HasPrefix(arg, "type=bind") {
			t.Fatalf("capture argv carries a host bind: %q", arg)
		}
		if (arg == "-v" || arg == "--volume") && i+1 < len(args) && strings.HasPrefix(args[i+1], "/") {
			t.Fatalf("capture argv binds a host path: %q", args[i+1])
		}
	}
}

// The pour's mount is the same minus readonly: copy-up stays disabled, because
// read-only alone does not stop the engine populating an empty volume from the
// helper image before mounting it.
func TestHelperArgvPourKeepsNocopyWithoutReadonly(t *testing.T) {
	h := Helper{
		Image:     "debian:bookworm",
		Volume:    "byre-proj-abc123-.claude",
		MountPath: "/.byre-helper-00",
		Script:    "tar -x -f - -C /.byre-helper-00",
	}
	got := strings.Join(helperArgs(Podman, h), " ")
	if !strings.Contains(got, " -v byre-proj-abc123-.claude:/.byre-helper-00:nocopy ") {
		t.Fatalf("pour mount must disable copy-up and not be read-only: %s", got)
	}
	if strings.Contains(got, ":ro") {
		t.Fatalf("pour mount must not be read-only: %s", got)
	}
}

// The identity's userns rides the helper on the keep-id path: a rootless
// Podman helper that skipped it would write the volume with remapped ids.
func TestHelperArgvCarriesKeepIDUserns(t *testing.T) {
	ident := Identity{UID: 1000, GID: 1000, KeepID: true}
	got := strings.Join(helperArgs(Podman, Helper{Image: "i", Identity: ident, Script: "x"}), " ")
	if !strings.Contains(got, "--userns="+ident.Userns()) {
		t.Fatalf("helper argv must carry the identity's userns (%s): %s", ident.Userns(), got)
	}
	// Podman turns an image-declared VOLUME into an image volume, which could
	// sit over or under the helper's mount path.
	if !strings.Contains(got, "--image-volume=ignore") {
		t.Fatalf("podman helper must ignore image volumes: %s", got)
	}
	if strings.Contains(strings.Join(helperArgs(Docker, Helper{Image: "i", Script: "x"}), " "), "--image-volume") {
		t.Fatal("docker has no --image-volume flag; it must not be passed there")
	}
}

// A preflight helper has no volume at all: nothing of the project is mounted
// while byre proves the image can run the command.
func TestHelperArgvNoVolumeNoMount(t *testing.T) {
	for _, arg := range helperArgs(Docker, Helper{Image: "i", MountPath: "/.byre-helper-00", Script: "x"}) {
		if arg == "--mount" || arg == "-v" || arg == "--volume" {
			t.Fatalf("a volumeless helper must mount nothing, and it passed %s", arg)
		}
	}
}
