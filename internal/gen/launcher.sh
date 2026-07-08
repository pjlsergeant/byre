#!/usr/bin/env bash
# byre launcher — the constant ENTRYPOINT.
#
# Runs UNPRIVILEGED as the in-box 'dev' user (the Dockerfile bakes that user to
# the host UID/GID and sets USER dev, so PID 1 here is already the runtime user).
# There is no root phase and no gosu drop: /home/dev and the named volumes are
# born owned by the baked UID at build time, so nothing needs re-owning. The
# launcher just places git identity + agent context, runs first-run hooks, and
# execs the agent — all as the same user.
set -euo pipefail

# The dev user's home is baked at build time (skills.DevHome); not an env knob —
# a run-time override would sidestep the context_target containment guarantee.
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
    echo "byre: (running this image without byre? the firewall sidecar must run alongside it — \`byre ejectfirewall\` prints it. To launch with NO walls instead: set BYRE_LAUNCH_GATE_FILE=/dev/null.)" >&2
    exit 1
  fi
fi

# git identity: mark the workspace safe so git doesn't refuse the bind-mounted
# repo (owned by the same uid, but git's dubious-ownership check is path-based).
git config --global --add safe.directory /workspace >/dev/null 2>&1 || true

# Place skill/agent context where the agent reads it. The target (e.g.
# /home/dev/.claude/CLAUDE.md) usually lives in a state volume that's only mounted
# now, at runtime — so this can't be a build-time COPY. Best-effort: a failure
# here must never block the launch.
if [ -s /etc/byre/agent-context-target ]; then
  CTX_TARGET="$(cat /etc/byre/agent-context-target)"
  if [ -n "$CTX_TARGET" ]; then
    # The agent's memory = skill [context], plus a --self-edit note when that grant
    # is actually present. The real signal is the project's byre.config mounted rw
    # at /home/dev/.byre-self (what --self-edit does) — NOT a spoofable env var.
    # We only TOUCH the memory file when we have something to place, so a run with
    # neither context nor self-edit leaves a persisted memory file untouched.
    # A symlink a prior agent run may have planted can't redirect the write: rm -f
    # drops it so we write a fresh regular file. Best-effort: never block launch.
    sh -c '
      t="$1"
      have_ctx=; [ -f /etc/byre/agent-context.md ] && have_ctx=1
      # self-edit grant = the store actually bind-mounted READ-WRITE at
      # /home/dev/.byre-self (what --self-edit does). Check /proc/mounts for an rw
      # mount at that target — not mere file existence (a baked files/ entry) nor a
      # read-only bind. (Deliberately rw-mounting something else at byre own internal
      # self-edit path is a self-granted, status-visible choice; the note is only
      # informational either way.)
      have_se=
      grep -Eq " /home/dev/\.byre-self [^ ]+ rw[, ]" /proc/mounts && [ -f /etc/byre/self-edit.md ] && have_se=1
      [ -n "$have_ctx" ] || [ -n "$have_se" ] || exit 0
      mkdir -p "$(dirname "$t")" || exit 0
      rm -f "$t"
      wrote=
      [ -n "$have_ctx" ] && cat /etc/byre/agent-context.md > "$t" && wrote=1
      if [ -n "$have_se" ]; then
        [ -n "$wrote" ] && printf "\n\n" >> "$t"
        cat /etc/byre/self-edit.md >> "$t" && wrote=1
      fi
      [ -n "$wrote" ] || rm -f "$t"
    ' _ "$CTX_TARGET"
  fi
fi 2>/dev/null || true

# First-run hooks — agent skills drop scripts here (M6). They run as the dev user
# (the launcher is unprivileged), so a hook does its own user-level setup directly
# (codex device-auth login → the .codex volume; devloop → /workspace). A hook that
# needs root is not supported: skills declaring privileged setup would need an
# explicit, status-visible grant, not a blanket-root entrypoint.
if [ -d /etc/byre/firstrun.d ]; then
  for hook in /etc/byre/firstrun.d/*; do
    [ -r "$hook" ] && bash "$hook" || true
  done
fi

# Launch env hooks — skills drop scripts here to put env into the AGENT
# process (a firstrun hook runs in its own process, so it can't). Sourced (not
# executed) in glob order, after firstrun hooks and immediately before exec,
# still as the unprivileged dev user. Best-effort per hook: a broken hook must
# never block the launch (errexit/nounset are suspended around the source; a
# hook must still never call `exit` -- sourced code exits the launcher). First
# user: claude-shared-auth exports CLAUDE_CODE_OAUTH_TOKEN from its identity
# volume (ADR 0017). The dir override is a test seam, per the gate precedent.
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
# /etc/byre/agent-cmd is an *executable script* an agent skill installs (M6);
# executing it (rather than word-splitting its text) preserves quoting/spaces.
if [ "$#" -gt 0 ]; then
  CMD=("$@")
elif [ -x /etc/byre/agent-cmd ]; then
  CMD=(/etc/byre/agent-cmd)
else
  CMD=(bash -l)
fi

exec "${CMD[@]}"
