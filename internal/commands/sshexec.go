package commands

import (
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/pjlsergeant/byre/internal/deliver"
)

// sshExec is deliver.SSHExec backed by the real ssh CLI. The remote command
// reaches the remote through the user's own ssh — config, keys, agents,
// ControlMaster settings and auth prompts all behave exactly as `ssh host`
// would (ssh prompts on /dev/tty, so a stdin busy with the tar stream never
// blocks authentication).
func sshExec(t deliver.SSHTarget, remoteArgv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	args := []string{}
	if t.Port != "" {
		args = append(args, "-p", t.Port)
	}
	// "--" ends option parsing: the destination can never be mistaken for a
	// flag, however it was spelled.
	args = append(args, "--", t.String(), shellQuoteJoin(remoteArgv))
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
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
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
