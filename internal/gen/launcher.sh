#!/usr/bin/env bash
# byre launcher — the constant ENTRYPOINT.
#
# Runs as root, maps the host UID/GID onto the in-box 'dev' user, re-owns byre's
# own storage to that user, then drops privileges via gosu and execs the agent.
# The box is the safety boundary; the agent runs unprivileged.
set -euo pipefail

: "${DEV_HOME:=/home/dev}"
UID_TARGET="${BYRE_UID:-1000}"
GID_TARGET="${BYRE_GID:-1000}"
# Where gen bakes the named-volume mount-point list (overridable for tests).
: "${BYRE_VOLUME_DIRS:=/etc/byre/volume-dirs}"
# The kernel mount table (overridable for tests). reown reads it to prune nested
# mounts out of the chown walk — see chown_tree.
: "${BYRE_PROC_MOUNTS:=/proc/mounts}"

# needs_chown: true iff DIR exists and isn't already owned by the runtime
# user+group. Makes the re-own idempotent — a no-op (and fast) once correct.
needs_chown() {
  [ -d "$1" ] || return 1
  [ "$(stat -c %u "$1" 2>/dev/null || echo -1)" != "$UID_TARGET" ] && return 0
  [ "$(stat -c %g "$1" 2>/dev/null || echo -1)" != "$GID_TARGET" ] && return 0
  return 1
}

# chown_tree re-owns DIR and everything beneath it that lives on DIR's OWN
# filesystem, to the runtime user — but NEVER a nested mount. This is the guard
# that keeps the privileged walk off HOST files: find -xdev won't *descend* into
# a mount on another device, but it still *prints* that mount point's own
# directory, and chowning that inode would change a host bind's ownership. So we
# prune every mount nested under DIR (authoritative list from the kernel mount
# table) — the boundary dirs are excluded from the walk entirely, at any depth.
# -xdev stays as a backstop for a mount that appears after we read the table, and
# chown -h never follows a symlink, so a planted link can't redirect the re-own.
#
# KNOWN LIMITATIONS (accepted; not enforced anywhere — byre's threat model is a
# single-user box where chown only ever lands on the user's OWN uid):
#   - the mountpoint is matched as the kernel prints it, so a target whose path
#     contains a space or backslash (/proc/mounts encodes these as \040 / \134)
#     would NOT be pruned. Config validation rejects control chars but allows
#     these, so the claim isn't enforced — just unlikely.
#   - only mounts NESTED under DIR are pruned ("$dir"/*); a host bind at EXACTLY
#     DIR (e.g. a bind over /home/dev itself) is walked from its root. That config
#     masks the agent's whole home and isn't something byre sets up.
chown_tree() {
  dir="$1"
  prunes=()
  while read -r _ mp _; do
    case "$mp" in
      "$dir"/*) prunes+=(-path "$mp" -prune -o) ;;
    esac
  done < "$BYRE_PROC_MOUNTS"
  find "$dir" ${prunes[@]+"${prunes[@]}"} -xdev -print0 2>/dev/null \
    | xargs -0 -r chown -h "$UID_TARGET:$GID_TARGET" 2>/dev/null || true
}

# reown_storage re-owns ONLY byre's own storage to the runtime user:
#   1. the dev home's own files (so the agent can write ~/.config, ~/.cache, …)
#   2. each named volume (so the agent can write state/cache, e.g. node_modules)
# and NOTHING else — each walk goes through chown_tree, which prunes any nested
# mount (a volume, the --self-edit config bind, a user bind) so the privileged
# chown never touches a HOST file.
reown_storage() {
  # 1. dev home's own files (nested mounts pruned by chown_tree).
  needs_chown "$DEV_HOME" && chown_tree "$DEV_HOME"

  # 2. each named volume, re-owned from its own root (its mount points are seeded
  # from the image as the build uid).
  if [ -r "$BYRE_VOLUME_DIRS" ]; then
    while IFS= read -r vol; do
      [ -n "$vol" ] || continue
      needs_chown "$vol" && chown_tree "$vol"
    done < "$BYRE_VOLUME_DIRS"
  fi

  # Never leak a probe's status as our own. needs_chown returns non-zero when a
  # dir is ALREADY correctly owned (the steady-state no-op), so on the common
  # idempotent run the last && short-circuits and this function would otherwise
  # return 1 — which, called bare under `set -e`, silently kills the launcher
  # before it execs the agent. The re-own is best-effort (chown_tree swallows its
  # own errors), so a clean exit here is always correct.
  return 0
}

# When sourced by a unit test, stop here: define the functions, run no main.
[ "${BYRE_LAUNCH_TEST:-}" = 1 ] && return 0

# ----------------------------- main (executed) -----------------------------

# Ensure a group/user exist for the host GID/UID by appending to passwd/group
# directly. This is a no-op when they already exist (e.g. build-time uid 1000 ==
# host uid), and avoids useradd's "uid outside UID_MIN..UID_MAX" warning when the
# host uid is low (e.g. 501 on macOS) — the mapping is still correct.
if ! getent group "$GID_TARGET" >/dev/null 2>&1; then
  echo "hostgroup:x:${GID_TARGET}:" >> /etc/group
fi
if ! getent passwd "$UID_TARGET" >/dev/null 2>&1; then
  echo "hostdev:x:${UID_TARGET}:${GID_TARGET}:byre:${DEV_HOME}:/bin/bash" >> /etc/passwd
fi

reown_storage

export HOME="$DEV_HOME"
# Run git config as the target user so ~/.gitconfig isn't created root-owned.
gosu "$UID_TARGET:$GID_TARGET" git config --global --add safe.directory /workspace >/dev/null 2>&1 || true

# Place skill/agent context where the agent reads it. The target (e.g.
# /home/dev/.claude/CLAUDE.md) usually lives in a state volume that's only mounted
# now, at runtime — so this can't be a build-time COPY. Best-effort: a failure
# here must never block the launch. Written dev-owned so the agent can read it.
if [ -s /etc/byre/agent-context-target ]; then
  CTX_TARGET="$(cat /etc/byre/agent-context-target)"
  if [ -n "$CTX_TARGET" ]; then
    # The agent's memory = skill [context], plus a --self-edit note when that grant
    # is actually present. The real signal is the project's byre.config mounted rw
    # at /home/dev/.byre-self (what --self-edit does) — NOT a spoofable env var.
    # We only TOUCH the memory file when we have something to place, so a run with
    # neither context nor self-edit leaves a persisted memory file untouched.
    # Done ENTIRELY AS DEV (never root): a symlink a prior agent run may have
    # planted can't redirect a privileged write; rm -f drops it so we write a fresh
    # regular file. Best-effort: a failure must never block launch.
    gosu "$UID_TARGET:$GID_TARGET" sh -c '
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

# First-run credential hooks — agent skills drop scripts here (M6). Each runs as
# root with BYRE_USER set so it can seed/check credentials before the drop.
if [ -d /etc/byre/firstrun.d ]; then
  for hook in /etc/byre/firstrun.d/*; do
    [ -r "$hook" ] && BYRE_USER="$UID_TARGET:$GID_TARGET" bash "$hook" || true
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

exec gosu "$UID_TARGET:$GID_TARGET" "${CMD[@]}"
