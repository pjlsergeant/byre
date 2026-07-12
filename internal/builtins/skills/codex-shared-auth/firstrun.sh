#!/bin/bash
# codex-shared-auth firstrun hook (ADR 0017) — idempotently asserts, EVERY
# launch, that $CODEX_HOME/auth.json is a symlink into the machine-wide
# identity volume. A dangling link is fine (the first `codex login` anywhere
# writes through it into the shared volume — Codex writes in place). Runs
# before the codex skill's login hook (00- prefix sorts first), so that hook
# sees either a valid shared credential or the expected dangling link.
# The base override is a test seam (the launcher's gate-file precedent).
IDENTITY_DIR="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/codex"
SHARED="$IDENTITY_DIR/auth.json"
export CODEX_HOME="${CODEX_HOME:-/home/dev/.codex-home}"
cred="$CODEX_HOME/auth.json"

mkdir -p "$IDENTITY_DIR" "$CODEX_HOME" 2>/dev/null || exit 0

# Adopt an existing per-project login rather than clobbering it: if this box
# already has a real auth.json and the shared copy doesn't exist yet, MOVE the
# file into the identity volume (it becomes the machine-wide credential).
if [ -f "$cred" ] && [ ! -L "$cred" ] && [ ! -e "$SHARED" ]; then
  # Say it out loud: this box's login is becoming THE machine credential.
  echo "byre codex-shared-auth: promoting this box's existing Codex login to the machine-wide shared credential" >&2
  mv "$cred" "$SHARED" 2>/dev/null || true
fi

# Assert the symlink. This also heals the logout-fork: `codex logout` deletes
# the symlink (not the target), and a later login would otherwise write a
# local file, silently forking off the shared credential. When both a local
# file AND a shared credential exist, the shared one wins (the local copy is
# a fork; discarding it is the healing).
if [ ! -L "$cred" ] || [ "$(readlink "$cred")" != "$SHARED" ]; then
  if [ -f "$cred" ] && [ ! -L "$cred" ] && [ -e "$SHARED" ]; then
    # Say it out loud: a local fork is being discarded for the shared login.
    echo "byre codex-shared-auth: replacing this box's local Codex login with the machine-wide shared credential (the local copy was a post-logout fork)" >&2
  fi
  rm -f "$cred"
  ln -s "$SHARED" "$cred" 2>/dev/null || true
fi
[ -f "$SHARED" ] && chmod 600 "$SHARED" 2>/dev/null || true
exit 0
