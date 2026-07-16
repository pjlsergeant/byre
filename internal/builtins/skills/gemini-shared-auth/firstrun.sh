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

# gemini-credentials.json is the 0.49+ encrypted credential (FileKeychain);
# oauth_creds.json is the legacy name -- link both, dangling is harmless.
for f in gemini-credentials.json oauth_creds.json google_accounts.json installation_id; do
  shared="$IDENTITY_DIR/$f"
  local_f="$GEMINI_DIR/$f"
  # Adopt an existing per-project login rather than clobbering it: a real
  # file with no shared copy MOVES in and becomes the machine-wide one.
  if [ -f "$local_f" ] && [ ! -L "$local_f" ] && [ ! -e "$shared" ]; then
    echo "byre gemini-shared-auth: promoting this box's $f to the machine-wide shared credential" >&2
    mv "$local_f" "$shared" 2>/dev/null || true
  fi
  # Assert the symlink; when both a local file and a shared copy exist, the
  # shared one wins (the local file is a fork; discarding it is the healing).
  if [ ! -L "$local_f" ] || [ "$(readlink "$local_f")" != "$shared" ]; then
    if [ -f "$local_f" ] && [ ! -L "$local_f" ] && [ -e "$shared" ]; then
      echo "byre gemini-shared-auth: replacing this box's local $f with the machine-wide shared copy (the local file was a fork)" >&2
    fi
    rm -f "$local_f"
    ln -s "$shared" "$local_f" 2>/dev/null || true
  fi
  [ -f "$shared" ] && chmod 600 "$shared" 2>/dev/null || true
done

# Seed the auth-method choice so gemini's auth-method DIALOG never opens. That
# dialog's clearCachedCredentialFile() rm's oauth_creds.json BEFORE writing the
# new login, and on our symlink the rm deletes the LINK -- so the first login
# writes a local regular file, silently forking off the shared volume (the
# 2026-07-16 field failure). Source-verified (gemini 0.51, npm bundle):
# clearCachedCredentialFile is called ONLY from the dialog's method-selection
# handler, never from the login path (initOauthClient/authWithUserCode) -- so a
# pre-set selectedType skips the dialog and the login writes THROUGH the intact
# link into the shared volume. oauth-personal is the shared-auth default (the
# subscription login this skill exists to share); it also removes the silent
# API-key-billing footgun (a saved key the picker would otherwise default onto).
# Seed only when UNSET -- never clobber a user's deliberate api-key choice.
settings="$GEMINI_DIR/settings.json"
if command -v jq >/dev/null 2>&1; then
  if [ ! -f "$settings" ]; then
    printf '%s\n' '{"security":{"auth":{"selectedType":"oauth-personal"}}}' > "$settings" 2>/dev/null || true
  else
    # Classify the current shape WITHOUT erroring on odd inputs: a
    # string-valued .security (or .security.auth) would make the object-merge
    # below fail silently and skip the seed (restoring the dialog-fork). "seed"
    # is emitted ONLY when both intermediates are absent-or-object, so the merge
    # is safe and LOSSLESS; a genuinely weird shape is left untouched (never
    # clobber user config) and announced (never silent).
    state=$(jq -r '
      def okobj(x): (x == null) or ((x | type) == "object");
      if (try (.security.auth.selectedType) catch null) != null then "set"
      elif (okobj(.security) and okobj(try .security.auth catch "x")) then "seed"
      else "weird" end
    ' "$settings" 2>/dev/null) || state=""
    if [ "$state" = "seed" ]; then
      tmp="$settings.byre.tmp"
      if jq '.security = ((.security // {}) + {auth: ((.security.auth // {}) + {selectedType: "oauth-personal"})})' \
        "$settings" > "$tmp" 2>/dev/null; then
        mv "$tmp" "$settings"
      else
        rm -f "$tmp"
      fi
    elif [ "$state" = "weird" ]; then
      echo "byre gemini-shared-auth: settings.json has an unexpected security/auth shape; not seeding selectedType (if the auth dialog strands the login, log in and relaunch)." >&2
    fi
  fi
fi
exit 0
