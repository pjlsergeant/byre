#!/usr/bin/env bash
# byre launcher — the constant ENTRYPOINT.
#
# Runs UNPRIVILEGED as the in-box 'dev' user (the Dockerfile bakes that user to
# the host UID/GID and sets USER dev, so PID 1 here is already the runtime user).
# There is no root phase and no gosu drop: /home/dev and the named volumes are
# born owned by the baked UID at build time, so nothing needs re-owning. The
# launcher just places git identity, exports the per-session context var,
# runs first-run hooks, and execs the agent — all as the same user. Agent
# context is INJECTED by the agent command (ADR 0046); the launcher writes no
# agent file.
set -euo pipefail

# The dev user's home is baked at build time (skills.DevHome); not an env
# knob — the chassis paths are constants, not configuration.
export HOME=/home/dev

# Launch gate — a network-posture skill (e.g. firewall) bakes a gate file whose
# content is a loopback port. When present, byre applies the skill's network
# setup from OUTSIDE the box (a netns-init helper container) after start, and
# that helper listens on the port once the rules are applied and verified. We
# poll-connect until it does, and only then proceed — so NOTHING in the box
# (context placement, first-run hooks, the agent) runs before the wall is up.
# Every failure path fails CLOSED: no listener within the timeout means the
# box exits instead of launching open. The handshake is deliberately stateless
# (no marker file): a `docker restart` recreates the netns without the rules,
# and this gate then simply times out again rather than trusting stale state.
# The env overrides exist for byre's own tests; a user setting them is
# disabling their own protection, which is theirs to do (footgun doctrine).
GATE_FILE="${BYRE_LAUNCH_GATE_FILE:-/etc/byre/launch-gate}"
if [ -s "$GATE_FILE" ]; then
  gate_port="$(tr -cd '0-9' < "$GATE_FILE")"
  gate_timeout="${BYRE_LAUNCH_GATE_TIMEOUT:-30}"
  gate_ok=
  SECONDS=0
  while [ "$SECONDS" -lt "$gate_timeout" ]; do
    # Bash's /dev/tcp: a successful connect means the netns-init helper is
    # listening, which it only does after its rules are applied and verified.
    if (exec 3<>"/dev/tcp/127.0.0.1/$gate_port") 2>/dev/null; then
      gate_ok=1
      break
    fi
    sleep 0.2
  done
  if [ -z "$gate_ok" ]; then
    echo "byre: launch gate: network setup never signaled ready on 127.0.0.1:${gate_port:-?} after ${gate_timeout}s — refusing to launch without it (failing closed)." >&2
    echo "byre: (running this image without byre? the firewall netns helper must run alongside it — \`byre ejectfirewall\` prints it. To launch with NO walls instead: set BYRE_LAUNCH_GATE_FILE=/dev/null.)" >&2
    exit 1
  fi
fi

# git identity: mark the workspace safe so git doesn't refuse the bind-mounted
# repo (owned by the same uid, but git's dubious-ownership check is path-based).
WS="${BYRE_WORKSPACE_DIR:-/workspace}"
git config --global --add safe.directory "$WS" >/dev/null 2>&1 || true

# Worktree populate: `byre worktree` registers the worktree --no-checkout in a
# one-shot creation container (runner.WorktreeAdd — every mutating git
# operation on the repo runs in a box, never on the host; ADR 0009), which
# drops a marker in the worktree git dir. The actual checkout runs HERE, in
# the box, where the repo's git extensions — the post-checkout hook,
# smudge/process filters — run contained like all its other code. Gated on the marker,
# so a normal box start (no marker) does nothing; the marker clears ONLY on a
# successful checkout, so a failed populate stays resumable on the next develop
# and never traps the user out of the box. Best-effort: a populate failure warns
# and still launches (an empty tree the user can fix beats no box).
#
# Detection needs NO git binary: a linked worktree's .git is a file
# "gitdir: <path>", and that path is bind-mounted into the box at its host path
# (same-path mounting, ADR 0009). Only the checkout itself needs git — so a box
# without git gets a loud, actionable message instead of a silently empty tree.
if [ -f "$WS/.git" ]; then
  wt_gitdir="$(sed -n 's/^gitdir: //p' "$WS/.git" 2>/dev/null | head -n1)"
  if [ -n "$wt_gitdir" ] && [ -f "$wt_gitdir/byre-needs-checkout" ]; then
    if ! command -v git >/dev/null 2>&1; then
      echo "byre: this worktree still needs to be checked out here, but the box has no git." >&2
      echo "byre: add 'git' to the box (byre config → Packages), then re-run 'byre develop' here." >&2
    elif git -C "$WS" checkout >&2; then
      echo "byre: populated the worktree checkout inside the box." >&2
      rm -f "$wt_gitdir/byre-needs-checkout"
    else
      echo "byre: could not fully populate the worktree checkout — the working tree may be empty or incomplete." >&2
      echo "byre: fix the cause and run 'git checkout' in the box, or re-run 'byre develop' here to retry." >&2
    fi
  elif [ -z "$(ls -A "$WS" 2>/dev/null | grep -v '^\.git$')" ]; then
    # A linked worktree with nothing but .git and NO pending marker: either a
    # marker a concurrent box deleted (a hint, not a source of truth; ADR 0009) or
    # a checkout that never happened. Surface it loudly rather than launch
    # silently into an empty tree. Not a block: the user may want the box.
    echo "byre: this worktree looks unpopulated and byre has no pending-checkout record for it." >&2
    echo "byre: if that's unexpected, run 'git checkout' here, or re-create it with 'byre worktree'." >&2
  fi
fi

# Per-session agent context — the DYNAMIC additions only: the egress
# allowlist this box actually enforces, and the --self-edit note when that
# grant is present. Exported as BYRE_SESSION_CONTEXT for the agent command
# to inject alongside the BAKED /etc/byre/agent-context.md (claude: the
# byre-claude-launch wrapper merges baked file + this var into one
# --append-system-prompt-file). byre never writes an agent-owned file to deliver prose (ADR
# 0046): the agent's memory file belongs to the user, and expropriating it
# was never byre's to do.
# Always exported (possibly empty), so an injecting command's
# "$BYRE_SESSION_CONTEXT" reference is safe unconditionally. Best-effort
# throughout: a failure composing informational text must never block the
# launch.
CTX_DIR="${BYRE_CONTEXT_DIR:-/etc/byre}"
BYRE_SESSION_CONTEXT=""
append_session_ctx() {
  [ -n "$1" ] || return 0
  if [ -n "$BYRE_SESSION_CONTEXT" ]; then
    BYRE_SESSION_CONTEXT="${BYRE_SESSION_CONTEXT}

$1"
  else
    BYRE_SESSION_CONTEXT="$1"
  fi
}
# Egress announcement: the wall is up (a posture skill baked the gate we
# already waited on) AND byre handed us the enforced allowlist — the same
# BYRE_EGRESS string the netns helper applied, so what we announce IS what
# is enforced. Informational only, hence an env var is fine here: a user
# setting it lies to their own agent (footgun doctrine).
if [ -s "$GATE_FILE" ] && [ -n "${BYRE_EGRESS+set}" ]; then
  if [ -n "$BYRE_EGRESS" ]; then
    eg_list="$(printf "%s" "$BYRE_EGRESS" | sed "s/ /, /g" 2>/dev/null || true)"
    append_session_ctx "## This session's egress allowlist

${eg_list}

Anything not listed is closed. The list was resolved when this session
started; a restart re-reads the config."
  else
    append_session_ctx "## This session's egress allowlist

The allowlist is EMPTY: every outbound connection is closed. A restart
re-reads the config."
  fi
fi
# self-edit grant = the store actually bind-mounted READ-WRITE at
# /home/dev/.byre-self (what --self-edit does). Check /proc/mounts for an rw
# mount at that target — not mere file existence (a baked files/ entry) nor a
# read-only bind. (Deliberately rw-mounting something else at byre'"'"'s own
# internal self-edit path is a self-granted, status-visible choice; the note
# is only informational either way.)
if grep -Eq " /home/dev/\.byre-self [^ ]+ rw[, ]" /proc/mounts 2>/dev/null && [ -f "$CTX_DIR/self-edit.md" ]; then
  append_session_ctx "$(cat "$CTX_DIR/self-edit.md" 2>/dev/null || true)"
fi
# Exported with a LEADING blank line when non-empty, so an adapter can
# concatenate baked+session directly ("$(cat agent-context.md)$BYRE_SESSION_CONTEXT")
# without separator logic of its own.
if [ -n "$BYRE_SESSION_CONTEXT" ]; then
  BYRE_SESSION_CONTEXT="

$BYRE_SESSION_CONTEXT"
fi
export BYRE_SESSION_CONTEXT

# First-run hooks — agent skills drop scripts here. They run as the dev user
# (the launcher is unprivileged), so a hook does its own user-level setup directly
# (codex device-auth login → the .codex volume; devlog → /workspace). A hook that
# needs root is not supported: skills declaring privileged setup would need an
# explicit, status-visible grant, not a blanket-root entrypoint. The dir
# override is a test seam (the gate-file/env.d precedent) — without it, the
# launcher tests execute the REAL hooks of whatever box runs the suite, and a
# hook that legitimately prompts (a login on a box whose credential died)
# hangs them.
FIRSTRUN_DIR="${BYRE_FIRSTRUN_DIR:-/etc/byre/firstrun.d}"
if [ -d "$FIRSTRUN_DIR" ]; then
  for hook in "$FIRSTRUN_DIR"/*; do
    # Unreadable entries -- and the literal "$FIRSTRUN_DIR/*" an unmatched glob
    # leaves behind -- are a silent no-op. A hook that RAN and failed is not:
    # the launcher continues (one skill's broken setup must not cost the user
    # their box) but says so, because a hook failing invisibly is how a box
    # boots subtly wrong. The `if bash ...; then :; else` shape is load-bearing
    # under `set -e`: the naive `bash "$hook"; status=$?` kills the launcher on
    # the failing hook, the exact inversion of best-effort.
    if [ -r "$hook" ]; then
      if bash "$hook"; then
        :
      else
        status=$?
        printf 'byre: firstrun hook %q exited %d (continuing)\n' "$hook" "$status" >&2
      fi
    fi
  done
fi

# Launch env hooks — skills drop scripts here to put env into the AGENT
# process (a firstrun hook runs in its own process, so it can't). Sourced (not
# executed) in glob order, after firstrun hooks and immediately before exec,
# still as the unprivileged dev user. Hooks owe this shell the ADR 0028 purity
# contract: the environment they leave behind is their only lasting effect.
# errexit/nounset are suspended around each source so strict mode does not turn
# a pure hook's benign unset reference into a dead launcher -- that suspension
# is a courtesy to hooks that KEEP the contract, not a container for ones that
# break it, and best-effort is guaranteed only to the former. First user:
# claude-shared-auth exports CLAUDE_CODE_OAUTH_TOKEN from its identity volume
# (ADR 0017). The dir override is a test seam, per the gate precedent.
ENVD_DIR="${BYRE_ENVD_DIR:-/etc/byre/env.d}"
if [ -d "$ENVD_DIR" ]; then
  for envhook in "$ENVD_DIR"/*.sh; do
    if [ -r "$envhook" ]; then
      set +eu
      # shellcheck disable=SC1090
      . "$envhook"
      set -eu
    fi
  done
fi

# Agent command: explicit run args > recorded agent command > login shell.
# /etc/byre/agent-cmd is an *executable script* an agent skill installs;
# executing it (rather than word-splitting its text) preserves quoting/spaces.
if [ "$#" -gt 0 ]; then
  CMD=("$@")
elif [ -x /etc/byre/agent-cmd ]; then
  CMD=(/etc/byre/agent-cmd)
else
  CMD=(bash -l)
fi

exec "${CMD[@]}"
