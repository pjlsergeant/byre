#!/bin/bash
# gemini-shared-auth firstrun hook (ADR 0017) — idempotently asserts, EVERY
# launch, that Gemini's identity files in ~/.gemini are symlinks into the
# machine-wide identity volume. Dangling links are fine (the first in-box
# OAuth login writes through them — Gemini writes in place). Per-project
# state (history/, tmp/, trustedFolders.json) is untouched. GATE PENDING:
# see skill.toml — this exists to RUN the rotation gate, not because the
# gate has passed. The dir overrides are test seams.
IDENTITY_DIR="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/gemini"
GEMINI_DIR="${BYRE_GEMINI_DIR:-/home/dev/.gemini}"

mkdir -p "$IDENTITY_DIR" "$GEMINI_DIR" 2>/dev/null || exit 0

for f in oauth_creds.json google_accounts.json installation_id; do
  shared="$IDENTITY_DIR/$f"
  local_f="$GEMINI_DIR/$f"
  # Adopt an existing per-project login rather than clobbering it: a real
  # file with no shared copy MOVES in and becomes the machine-wide one.
  if [ -f "$local_f" ] && [ ! -L "$local_f" ] && [ ! -e "$shared" ]; then
    mv "$local_f" "$shared" 2>/dev/null || true
  fi
  # Assert the symlink; when both a local file and a shared copy exist, the
  # shared one wins (the local file is a fork; discarding it is the healing).
  if [ ! -L "$local_f" ] || [ "$(readlink "$local_f")" != "$shared" ]; then
    rm -f "$local_f"
    ln -s "$shared" "$local_f" 2>/dev/null || true
  fi
  [ -f "$shared" ] && chmod 600 "$shared" 2>/dev/null || true
done
exit 0
