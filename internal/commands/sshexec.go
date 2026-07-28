package commands

import (
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostexec"
)

// sshExecWith returns a deliver.SSHExec backed by the real ssh CLI. The remote
// command reaches the remote through the user's own ssh — config, keys,
// agents, ControlMaster settings and auth prompts all behave exactly as `ssh
// host` would (ssh prompts on /dev/tty, so a stdin busy with the tar stream
// never blocks authentication).
//
// The ssh BINARY is resolved once through hostexec against the project's
// box-writable roots: an ssh sitting in the project tree would carry the
// delivery's payload, its stdin, and the user's credentials to a host of the
// agent's choosing, so a refusal here fails the delivery by name rather than
// letting it proceed.
func sshExecWith(roots hostexec.Roots) deliver.SSHExec {
	return func(t deliver.SSHTarget, remoteArgv []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return sshExec(roots, t, remoteArgv, stdin, stdout, stderr)
	}
}

func sshExec(roots hostexec.Roots, t deliver.SSHTarget, remoteArgv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	exe, err := hostexec.Look("ssh", roots)
	if err != nil {
		return err
	}
	args := []string{}
	if t.Port != "" {
		args = append(args, "-p", t.Port)
	}
	// "--" ends option parsing: the destination can never be mistaken for a
	// flag, however it was spelled.
	args = append(args, "--", t.String(), shellQuoteJoin(remoteArgv))
	cmd := exec.Command(exe, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &deliver.SSHExitError{Code: ee.ExitCode()}
	}
	return err
}

// shellQuoteJoin renders argv for the remote shell: each token single-quoted
// (' itself via the '\” dance), joined with spaces. ssh concatenates its
// command arguments into one string a remote shell evaluates — quoting here
// is what keeps a --box value or a byre path with spaces intact, whatever
// shell answers on the far side.
func shellQuoteJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = gen.ShellQuote(a)
	}
	return strings.Join(quoted, " ")
}
