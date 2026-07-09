#!/bin/bash
# grok-shared-auth firstrun hook (ADR 0017) — idempotently asserts, EVERY
# launch, that $GROK_HOME/auth.json is a symlink into the machine-wide
# identity volume. A dangling link is fine (the first `grok login
# --device-auth` anywhere writes through it — GATE PENDING: grok is closed
# source and the write-through-symlink claim is unverified; see skill.toml).
# Runs before the grok skill's login hook (00- prefix sorts first), so that
# hook sees either a valid shared credential or the expected dangling link.
# The base override is a test seam (the launcher's gate-file precedent).
IDENTITY_DIR="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/grok"
SHARED="$IDENTITY_DIR/auth.json"
export GROK_HOME="${GROK_HOME:-/home/dev/.grok-home}"
cred="$GROK_HOME/auth.json"

mkdir -p "$IDENTITY_DIR" "$GROK_HOME" 2>/dev/null || exit 0

# Adopt an existing per-project login rather than clobbering it: if this box
# already has a real auth.json and the shared copy doesn't exist yet, MOVE the
# file into the identity volume (it becomes the machine-wide credential).
if [ -f "$cred" ] && [ ! -L "$cred" ] && [ ! -e "$SHARED" ]; then
  mv "$cred" "$SHARED" 2>/dev/null || true
fi

# Assert the symlink. This also heals the logout-fork: `grok logout` clears
# the credential, and a later login would otherwise write a local file,
# silently forking off the shared credential. When both a local file AND a
# shared credential exist, the shared one wins (the local copy is a fork;
# discarding it is the healing).
if [ ! -L "$cred" ] || [ "$(readlink "$cred")" != "$SHARED" ]; then
  rm -f "$cred"
  ln -s "$SHARED" "$cred" 2>/dev/null || true
fi
[ -f "$SHARED" ] && chmod 600 "$SHARED" 2>/dev/null || true
exit 0
