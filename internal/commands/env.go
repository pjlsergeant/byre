package commands

import (
	"os/exec"
	"strings"
)

// addGitIdentity copies only the host git user.name/user.email into env as the
// GIT_*_NAME/EMAIL vars — the one narrow exception to host-env isolation.
func addGitIdentity(env map[string]string) {
	if name := gitConfig("user.name"); name != "" {
		env["GIT_AUTHOR_NAME"] = name
		env["GIT_COMMITTER_NAME"] = name
	}
	if email := gitConfig("user.email"); email != "" {
		env["GIT_AUTHOR_EMAIL"] = email
		env["GIT_COMMITTER_EMAIL"] = email
	}
}

func gitConfig(key string) string {
	out, err := exec.Command("git", "config", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
