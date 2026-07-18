package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pjlsergeant/byre/internal/build"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// Dockerfile implements `byre dockerfile`: resolve identity, resolve the config
// cascade + skills, render the Dockerfile in memory, and print it. Side-effect-
// free for the project: ValidateExisting (not Bootstrap) keeps the collision
// check without enrolling an uninitialized project in ~/.byre/projects, just as
// build.Render keeps the build context untouched.
func Dockerfile(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	rv, err := resolve(paths, projectDir, s.Err)
	if err != nil {
		return err
	}
	df, err := build.Render(paths, rv.cfg, rv.skills)
	if err != nil {
		return err
	}
	// Ejection legibility (ADR 0019): a firewalled image gates on byre's
	// launch-time handshake and refuses to start without it — the printed
	// artifact must explain its own booby trap. Command-layer prepend, so the
	// generated Dockerfile (and its golden test) stays byte-identical.
	if len(rv.skills.NetnsInits()) > 0 {
		fmt.Fprint(s.Out, ejectGateComment)
	}
	// A `files` entry shadowing a byre-managed security path is overridden by the
	// tail guard; say so on stderr so the printed artifact isn't read as if the
	// clobber took effect (stdout stays the clean Dockerfile).
	warnGuardCollisions(s.Err, rv.cfg, rv.skills)
	_, err = io.WriteString(s.Out, df)
	return err
}

// ejectGateComment heads `byre dockerfile` output when a netns hook (the
// firewall) is enabled: outside byre this image fails closed at its launch
// gate, and the reader deserves to learn that from the artifact itself.
const ejectGateComment = `# NOTE (byre): this image expects byre's launch-time firewall.
# A netns hook (the firewall skill) applies egress rules from OUTSIDE the
# container at launch; the baked launch gate (/etc/byre/launch-gate) waits for
# that and exits after ~30s if it never comes -- failing closed rather than
# running unwalled. Ejected from byre, either:
#   - replay the sidecar yourself: byre ejectfirewall > firewall.sh
#   - or run WITHOUT the walls: delete /etc/byre/launch-gate from the image
#     (or set BYRE_LAUNCH_GATE_FILE=/dev/null), accepting an open network.

`

// DockerRun implements `byre dockerrun`: print the `docker run` (or `podman run`)
// invocation byre would use for this project — the run-time counterpart to
// `byre dockerfile`. Informational and side-effect-free: it resolves config +
// skills and assembles the exact argv (env, workspace bind, mounts, volumes,
// ports, caps, raw run_args, label, image), but builds/runs nothing, so it works
// even before the image exists or without the engine on PATH.
func DockerRun(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	// Same guard develop applies: a comma in a bind source can't be expressed in
	// a docker --mount, so don't advertise a command develop would refuse to run.
	if err := checkMountPaths(paths); err != nil {
		return err
	}
	rv, err := resolve(paths, projectDir, s.Err)
	if err != nil {
		return err
	}
	// Best-effort engine name for the leading token; fall back to the configured
	// value (or docker) so this stays informational when no engine is installed.
	// The identity follows the engine (keep-id under rootless Podman) so the
	// printed argv matches what develop would run — host identity when no
	// engine is reachable.
	engine := orDefault(rv.cfg.Engine, "docker")
	ident := hostIdentity()
	if eng, derr := runner.Detect(rv.cfg.Engine, nil); derr == nil {
		engine = string(eng)
		ident = engineIdentity(runner.New(eng), os.Getuid(), os.Getgid())
	}
	image := imageTag(paths.ID, ident.UID, ident.GID)
	params, err := runParams(paths, rv, image, false, s.TTY, ident)
	if err != nil {
		return err
	}
	argv := append([]string{engine}, runner.RunArgs(params)...)
	fmt.Fprintln(s.Out, shellCommand(argv))
	// Ejection legibility (ADR 0019): with the firewall enabled, this command
	// alone yields a box that dies at its launch gate. Stderr, so the
	// copy-pasteable stdout line stays clean.
	if len(rv.skills.NetnsInits()) > 0 {
		fmt.Fprintln(s.Err, "byre: note — this project runs a firewall byre applies at launch; started with just this command, the box fails closed at its launch gate (~30s). `byre ejectfirewall` prints the sidecar to run alongside it.")
	}
	return nil
}

// EjectFirewall implements `byre ejectfirewall`: print, as a standalone shell
// script, the firewall sidecar byre runs for this project — the one piece of
// the box `byre dockerfile` + `byre dockerrun` cannot carry (ADR 0019), made
// portable. The enforcement script already ships inside the image; ejected,
// the user replays byre's own invocation of it against their running box.
func EjectFirewall(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	rv, err := resolve(paths, projectDir, s.Err)
	if err != nil {
		return err
	}
	hooks := rv.skills.NetnsInits()
	if len(hooks) == 0 {
		return fmt.Errorf("no netns hooks (firewall) enabled for this project — nothing to eject")
	}
	engine := orDefault(rv.cfg.Engine, "docker")
	ident := hostIdentity()
	if eng, derr := runner.Detect(rv.cfg.Engine, nil); derr == nil {
		engine = string(eng)
		ident = engineIdentity(runner.New(eng), os.Getuid(), os.Getgid())
	}
	image := imageTag(paths.ID, ident.UID, ident.GID)

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# byre ejectfirewall — the firewall sidecar byre runs for " + paths.ID + ",\n")
	b.WriteString("# as a standalone script. Start the box first (see `byre dockerrun`); it\n")
	b.WriteString("# waits at its launch gate (~30s) for this to apply and verify the egress\n")
	b.WriteString("# rules, and proceeds only then (no rules = the box exits, failing closed).\n")
	b.WriteString("# The allowlist below was resolved by byre (skills' egress + the config\n")
	b.WriteString("# `egress` key); edit it here as your needs change.\n")
	b.WriteString("set -e\n")
	b.WriteString(`BOX="${1:?usage: $0 <container id or name> (the just-started box)}"` + "\n")
	for _, h := range hooks {
		b.WriteString(shellCommand([]string{
			engine, "run", "--rm", "-u", "0:0", "--cap-add", "NET_ADMIN",
			"--entrypoint", h.Path,
			"-e", "BYRE_EGRESS=" + strings.Join(resolvedEgress(rv), " "),
			"-e", "BYRE_EGRESS_DENY=" + strings.Join(rv.cfg.EgressClosed, " "),
		}))
		// Keep-id boxes (rootless Podman) own their netns from inside their
		// userns; the sidecar must join it or iptables gets EPERM (mirrors
		// runner.NetnsInit).
		if ident.KeepID {
			b.WriteString(` --userns "container:$BOX"`)
		}
		b.WriteString(` --net "container:$BOX" ` + shellArg(image) + "\n")
	}
	_, err = io.WriteString(s.Out, b.String())
	return err
}

// shellCommand renders an argv as a copy-pasteable shell command line, quoting
// only the args that need it.
func shellCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellArg(a)
	}
	return strings.Join(quoted, " ")
}

// shellArg single-quotes an argument when it contains shell-significant
// characters, escaping embedded single quotes; leaves plain args (including the
// = and , that fill docker --mount/-e specs) bare for readability.
func shellArg(s string) string {
	// Includes brace/bracket/tilde/bang/hash so a shell can't expand a raw run
	// arg (e.g. --flag={a,b}) into different argv than develop's exec would pass.
	// = , : / . - _ @ stay bare so --mount/-e specs read cleanly.
	const unsafe = " \t\n\"'$\\|&;<>*?(){}[]~!#"
	if s != "" && !strings.ContainsAny(s, unsafe) && !strings.ContainsRune(s, '`') {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
