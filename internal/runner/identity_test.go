package runner

import (
	"errors"
	"strings"
	"testing"
)

func TestIdentityUserns(t *testing.T) {
	if got := (Identity{UID: 501, GID: 20}).Userns(); got != "" {
		t.Errorf("rootful identity must carry no userns, got %q", got)
	}
	// The explicit uid=/gid= form is the contract: plain keep-id only aligns
	// when the host uid already equals the image uid (ADR 0008).
	if got := (Identity{UID: 1000, GID: 1000, KeepID: true}).Userns(); got != "keep-id:uid=1000,gid=1000" {
		t.Errorf("keep-id userns = %q", got)
	}
}

func TestPodmanVersionAtLeast(t *testing.T) {
	cases := map[string]bool{
		"podman version 4.9.3":     true,
		"podman version 5.0.2":     true,
		"podman version 4.3.0":     true,
		"podman version 4.3.0-rc1": true,
		"podman version 4.2.1":     false,
		"podman version 3.4.4":     false,
		"":                         false,
		"garbage":                  false,
		"podman version x.y.z":     false,
	}
	for in, want := range cases {
		if got := podmanVersionAtLeast(in, 4, 3); got != want {
			t.Errorf("podmanVersionAtLeast(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSupportsKeepIDMapping(t *testing.T) {
	// Non-podman engines never claim support (and never exec anything).
	r := &Runner{engine: Docker, capture: func(name string, args ...string) (string, error) {
		t.Fatal("docker must not be probed")
		return "", nil
	}}
	if ok, err := r.SupportsKeepIDMapping(); ok || err != nil {
		t.Fatalf("docker SupportsKeepIDMapping = %v, %v", ok, err)
	}

	r = &Runner{engine: Podman, capture: func(name string, args ...string) (string, error) {
		if name != "podman" || len(args) != 1 || args[0] != "--version" {
			t.Fatalf("unexpected probe argv: %s %v", name, args)
		}
		return "podman version 5.0.2\n", nil
	}}
	if ok, err := r.SupportsKeepIDMapping(); !ok || err != nil {
		t.Fatalf("podman 5.0.2 SupportsKeepIDMapping = %v, %v", ok, err)
	}

	r = &Runner{engine: Podman, capture: func(string, ...string) (string, error) {
		return "podman version 4.2.1\n", nil
	}}
	if ok, _ := r.SupportsKeepIDMapping(); ok {
		t.Fatal("podman 4.2.1 must not claim keep-id mapping support")
	}

	r = &Runner{engine: Podman, capture: func(string, ...string) (string, error) {
		return "", errors.New("boom")
	}}
	if _, err := r.SupportsKeepIDMapping(); err == nil {
		t.Fatal("a probe error must be returned, not guessed away")
	}
}

// Every helper that fills a keep-id box's volumes must run inside the same
// mapping (chown targets only mean the same thing inside one userns), and the
// box's own run argv must carry the flag before raw run_args (last-wins stays
// with the author).
func TestKeepIDUsernsOnHelpers(t *testing.T) {
	id := Identity{UID: 1000, GID: 1000, KeepID: true}
	const flag = "--userns=keep-id:uid=1000,gid=1000"

	r, gotArgs := argvRunner(Podman)
	if err := r.SeedVolume("v", "/src", "img", id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*gotArgs, " "); !strings.Contains(got, flag) || !strings.Contains(got, "chown -R 1000:1000") {
		t.Errorf("SeedVolume argv missing keep-id userns or chown: %q", got)
	}

	r, gotArgs = argvRunner(Podman)
	if err := r.SeedFiles("v", "/src", []string{"a"}, "img", id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*gotArgs, " "); !strings.Contains(got, flag) {
		t.Errorf("SeedFiles argv missing keep-id userns: %q", got)
	}

	r, gotArgs = argvRunner(Podman)
	if err := r.MigrateVolume("a", "b", "img", id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*gotArgs, " "); !strings.Contains(got, flag) {
		t.Errorf("MigrateVolume argv missing keep-id userns: %q", got)
	}

	var capArgs []string
	rc := &Runner{engine: Podman, streamIn: nil, capture: func(name string, args ...string) (string, error) {
		capArgs = append([]string{name}, args...)
		return "989\n", nil
	}}
	if _, err := rc.ProbeSockGroup("img", "/h", "/t", id.Userns()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(capArgs, " "); !strings.Contains(got, flag) {
		t.Errorf("ProbeSockGroup argv missing keep-id userns: %q", got)
	}
}

// The netns helper joins the BOX's user namespace, not a fresh identical
// mapping: a netns is owned by the userns that created it, and NET_ADMIN over
// it only exists inside that owner.
func TestNetnsInitJoinsBoxUserns(t *testing.T) {
	var gotArgs []string
	r := &Runner{engine: Podman, capture: func(name string, args ...string) (string, error) {
		gotArgs = append([]string{name}, args...)
		return "", nil
	}}
	if err := r.NetnsInit("img", "byre-box", "/fw", nil, true); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--userns=container:byre-box") {
		t.Errorf("NetnsInit argv must join the box's userns: %q", joined)
	}
	if !strings.Contains(joined, "--net container:byre-box") {
		t.Errorf("NetnsInit argv must still join the box's netns: %q", joined)
	}
}

func TestRunArgsUsernsBeforeRawRunArgs(t *testing.T) {
	p := RunParams{
		Image:   "img",
		Userns:  "keep-id:uid=1000,gid=1000",
		RunArgs: []string{"--userns=host"},
	}
	args := RunArgs(p)
	joined := strings.Join(args, " ")
	iByre := strings.Index(joined, "--userns=keep-id:uid=1000,gid=1000")
	iRaw := strings.Index(joined, "--userns=host")
	if iByre < 0 {
		t.Fatalf("byre's --userns missing: %q", joined)
	}
	if iRaw < iByre {
		t.Errorf("raw run_args must come after byre's userns (last-wins): %q", joined)
	}
}
