package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"byre/internal/build"
	"byre/internal/project"
	"byre/internal/runner"
)

// Dockerfile implements `byre dockerfile`: resolve identity, resolve the config
// cascade + skills, generate the Dockerfile into the build context, and print it.
func Dockerfile(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	rv, err := resolve(paths, projectDir)
	if err != nil {
		return err
	}
	if rv.cfg.Dockerfile != "" {
		// Opt-out: byre doesn't generate; show the user's hand-written Dockerfile.
		dfPath, rerr := resolveProjectFile(paths.Canonical, rv.cfg.Dockerfile)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(dfPath)
		if rerr != nil {
			return fmt.Errorf("dockerfile %q: %w", rv.cfg.Dockerfile, rerr)
		}
		fmt.Fprintf(s.Out, "# byre: generation opted out; using %s verbatim:\n", rv.cfg.Dockerfile)
		_, err = s.Out.Write(b)
		return err
	}
	df, err := build.Render(paths, rv.cfg, rv.skills)
	if err != nil {
		return err
	}
	_, err = io.WriteString(s.Out, df)
	return err
}

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
	if err := paths.Bootstrap(); err != nil {
		return err
	}
	// Same guard develop applies: a comma in a bind source can't be expressed in
	// a docker --mount, so don't advertise a command develop would refuse to run.
	if err := checkMountPaths(paths); err != nil {
		return err
	}
	rv, err := resolve(paths, projectDir)
	if err != nil {
		return err
	}
	image := ImageTag(paths.ID, os.Getuid(), os.Getgid())
	params, err := runParams(paths, rv, image, false, s.TTY)
	if err != nil {
		return err
	}
	// Best-effort engine name for the leading token; fall back to the configured
	// value (or docker) so this stays informational when no engine is installed.
	engine := orDefault(rv.cfg.Engine, "docker")
	if eng, derr := runner.Detect(rv.cfg.Engine, nil); derr == nil {
		engine = string(eng)
	}
	argv := append([]string{engine}, runner.RunArgs(params)...)
	fmt.Fprintln(s.Out, shellCommand(argv))
	return nil
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
