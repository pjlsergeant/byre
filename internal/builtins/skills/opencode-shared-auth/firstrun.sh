#!/bin/bash
# opencode-shared-auth firstrun hook — idempotently asserts, EVERY launch,
# that the opencode data-dir auth.json is a symlink into the machine-wide
# identity volume. A dangling link is fine (the first `opencode auth login`
# anywhere writes through it into the shared volume — opencode writes in
# place; write-through verified live 2026-07-16). Runs before the opencode
# skill's login hook (00- prefix sorts first), so that hook sees either a
# valid shared credential or the expected dangling link. Mirrors
# codex-shared-auth; the base overrides are test seams (the launcher's
# gate-file precedent). XDG_DATA_HOME is opencode's own data-dir relocation
# env, so honoring it here stays faithful to the CLI's resolution.
IDENTITY_DIR="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/opencode"
SHARED="$IDENTITY_DIR/auth.json"
DATA_DIR="${XDG_DATA_HOME:-/home/dev/.local/share}/opencode"
cred="$DATA_DIR/auth.json"

# Failing to create either dir means shared auth cannot be asserted this
# launch; say so before degrading (best-effort, never block the launch) —
# otherwise the fallback to a per-project login is silent and the user
# believes the machine-wide credential is in play.
if ! mkdir -p "$IDENTITY_DIR" "$DATA_DIR" 2>/dev/null; then
  echo "byre opencode-shared-auth: cannot create $IDENTITY_DIR or $DATA_DIR — shared auth not asserted this launch (falling back to a per-project login)." >&2
  exit 0
fi

# Adopt an existing per-project login rather than clobbering it: if this box
# already has a real auth.json and the shared copy doesn't exist yet, MOVE
# the file into the identity volume (it becomes the machine-wide credential).
if [ -f "$cred" ] && [ ! -L "$cred" ] && [ ! -e "$SHARED" ]; then
  # Say it out loud: this box's login is becoming THE machine credential.
  echo "byre opencode-shared-auth: promoting this box's existing OpenCode login to the machine-wide shared credential" >&2
  mv "$cred" "$SHARED" 2>/dev/null || true
fi

# Assert the symlink. This also heals the logout-fork: `opencode auth
# logout` rewrites/removes entries in the local file, and a later login
# would otherwise write a local file, silently forking off the shared
# credential. When both a local file AND a shared credential exist, the
# shared one wins (the local copy is a fork; discarding it is the healing).
if [ ! -L "$cred" ] || [ "$(readlink "$cred")" != "$SHARED" ]; then
  if [ -f "$cred" ] && [ ! -L "$cred" ] && [ -e "$SHARED" ]; then
    # Say it out loud: a local fork is being discarded for the shared login.
    echo "byre opencode-shared-auth: replacing this box's local OpenCode login with the machine-wide shared credential (the local copy was a post-logout fork)" >&2
  fi
  rm -f "$cred"
  ln -s "$SHARED" "$cred" 2>/dev/null || true
fi
[ -f "$SHARED" ] && chmod 600 "$SHARED" 2>/dev/null || true

# API-key logins only. This skill shares ONE auth.json across boxes; OAuth
# entries (Claude Pro/Max, Copilot, ...) rotate a single-use refresh token that
# concurrent boxes cannot safely share (they race and cascade-logout). API keys
# are static and share cleanly. If the shared store holds an OAuth entry, say so
# -- friendly, and NEVER touch it: it's a live working credential, and quarantining
# it would be the grok-v1 heal-that-clobbers mistake. auth.json is written with
# JSON.stringify(...,2), so entries read `"type": "oauth"`; tolerate spacing.
if [ -f "$SHARED" ] && grep -Eq '"type"[[:space:]]*:[[:space:]]*"oauth"' "$SHARED" 2>/dev/null; then
  echo "byre opencode-shared-auth: this skill shares API-key logins only. The shared store has an OAuth login (e.g. Claude Pro/Max), which misbehaves when multiple boxes refresh it — log in with an API key for that provider instead." >&2
fi
exit 0
