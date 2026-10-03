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

# The filesystem primitives (identity route, shared-path vetting, the link
# assert, the exclusive promote) live in one library five skills ship to this
# path; this hook owns the policy and the messages. BYRE_SHARED_AUTH_LIB is a
# test seam, not a user knob: byre refuses BYRE_* names in a project [env]
# (internal/config/config.go). A library byre cannot read means none of the
# hardening below is in force, which is never something to do quietly.
BYRE_SA_LIB="${BYRE_SHARED_AUTH_LIB:-/usr/local/lib/byre-shared-auth-lib.sh}"
if [ ! -r "$BYRE_SA_LIB" ]; then
  echo "byre gemini-shared-auth: cannot read the shared-auth library ($BYRE_SA_LIB) — shared auth not asserted this launch." >&2
  exit 0
fi
. "$BYRE_SA_LIB"

# A symlinked identity DIR -- or any symlinked ancestor -- is not ours:
# linking the identity files through it would send every login on this
# machine wherever the planted link points. Refuse, and leave any per-project
# login untouched.
if ! byre_sa_identity_route_safe "$IDENTITY_DIR"; then
  echo "byre gemini-shared-auth: refusing: the identity dir ($IDENTITY_DIR) is or resolves through a symlink — shared auth not asserted this launch." >&2
  exit 0
fi

# Failing to create either dir means shared auth cannot be asserted this
# launch; say so before degrading (best-effort, never block the launch) —
# otherwise the fallback to a per-project login is silent and the user
# believes the machine-wide credential is in play.
if ! mkdir -p -- "$IDENTITY_DIR" "$GEMINI_DIR" 2>/dev/null; then
  echo "byre gemini-shared-auth: cannot create $IDENTITY_DIR or $GEMINI_DIR — shared auth not asserted this launch (falling back to a per-project login)." >&2
  exit 0
fi

# Re-checked on the dir itself now it exists: a link raced in during mkdir -p
# carries the shared credentials off the same way.
if ! byre_sa_path_as_spelled "$IDENTITY_DIR"; then
  echo "byre gemini-shared-auth: refusing: the identity dir ($IDENTITY_DIR) resolves through a symlink — shared auth not asserted this launch." >&2
  exit 0
fi

# refuse_shared FILE SHARED-PATH — one wording for the shared object this
# hook may not link to, said from three places in assert_shared.
refuse_shared() {
  echo "byre gemini-shared-auth: refusing: the shared $1 ($2) is a symlink or not a regular file — not linked this launch; remove it in byre shell." >&2
}

# assert_shared FILE — promote, then link, one identity file. A refusal or
# failure skips THIS file (return) and the loop moves on: one bad file must
# not cost the others their link.
assert_shared() {
  f=$1
  shared="$IDENTITY_DIR/$f"
  local_f="$GEMINI_DIR/$f"
  if byre_sa_shared_unsafe "$shared"; then
    refuse_shared "$f" "$shared"
    return 0
  fi

  # Adopt an existing per-project login rather than clobbering it: a
  # NON-EMPTY real file with no shared copy becomes the machine-wide one. (An
  # empty one is no login to announce, and the link below replaces it like
  # any other local file.) The claim is exclusive, so two boxes launching at
  # once cannot overwrite each other; a failed claim keeps the local file and
  # skips the link, since falling through would discard the only login and
  # link to nothing.
  promoted=""
  lost=""
  if [ -f "$local_f" ] && [ ! -L "$local_f" ] && [ -s "$local_f" ] && [ ! -e "$shared" ]; then
    byre_sa_promote "$local_f" "$shared"
    case $? in
    0)
      promoted=1
      echo "byre gemini-shared-auth: promoted this box's $f to the machine-wide shared credential" >&2
      ;;
    2)
      # Lost the race: shared wins, as the heal policy below says — re-vetted,
      # since the check above ran before it existed.
      if byre_sa_shared_unsafe "$shared"; then
        refuse_shared "$f" "$shared"
        return 0
      fi
      lost=1
      ;;
    *)
      echo "byre gemini-shared-auth: could not promote this box's $f into the identity volume; keeping the per-project file — not linked this launch." >&2
      return 0
      ;;
    esac
  fi

  # Assert the symlink; when both a local file and a shared copy exist, the
  # shared one wins (the local file is a fork; discarding it is the healing).
  discard=""
  [ -z "$promoted" ] && [ -f "$local_f" ] && [ ! -L "$local_f" ] && [ -e "$shared" ] && discard=1
  byre_sa_assert_link "$local_f" "$shared"
  case $? in
  0) ;;
  2)
    refuse_shared "$f" "$shared"
    return 0
    ;;
  3)
    echo "byre gemini-shared-auth: refusing: $local_f is not a regular file — not linked this launch; inspect it in byre shell." >&2
    return 0
    ;;
  *)
    echo "byre gemini-shared-auth: could not link $f to the shared copy; this box keeps its local file — not linked this launch." >&2
    return 0
    ;;
  esac
  if [ -n "$lost" ]; then
    echo "byre gemini-shared-auth: another box promoted its $f first; this box's local file was replaced by the shared one" >&2
  elif [ -n "$discard" ]; then
    echo "byre gemini-shared-auth: replaced this box's local $f with the machine-wide shared copy (the local file was a fork)" >&2
  fi
  return 0
}

# oauth_creds.json is the default PLAINTEXT OAuth store; gemini-credentials.json
# is the opt-in encrypted one (FileKeychain) and what the API-key store always
# uses (see skill.toml) -- link both, dangling is harmless.
for f in gemini-credentials.json oauth_creds.json google_accounts.json installation_id; do
  assert_shared "$f"
  [ -f "$IDENTITY_DIR/$f" ] && [ ! -L "$IDENTITY_DIR/$f" ] && chmod -- 600 "$IDENTITY_DIR/$f" 2>/dev/null || true
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
# And only into a regular file or an absent path: anything else (a symlink
# included, dangling or not) is left alone and named, because opening it would
# block the launch on a FIFO or write through the link.
settings="$GEMINI_DIR/settings.json"

# not_seeding REASON — one wording for every shape the seed declines or
# cannot write, since the user's remedy is the same in all of them.
not_seeding() {
  echo "byre gemini-shared-auth: $1; not seeding selectedType (if the auth dialog strands the login, log in and relaunch)." >&2
}

# seed_settings COMMAND... — write the seed through a fresh mktemp name and
# rename it over settings.json, never `>` the path itself: a FIFO or symlink
# that appears there after the check would block or be written through, and a
# FIXED temp name is itself a plantable path.
#
# The rename takes no `mv -T` (GNU-only), so a symlink to a DIRECTORY raced in
# after the check does not fail it: mv follows the link and deposits the temp
# file inside the target -- the merged settings.json, which is the user's own
# file and holds a stored API key if they have one. The rename therefore counts
# only once settings.json is checked to be a regular non-symlink file, and the
# refusal removes the name the deposit left inside that directory as well as
# the temp (byre_sa_drop_interior says why reaching through the link is safe
# there and nowhere else). Said once, whichever step failed.
seed_settings() {
  tmp=$(mktemp "$GEMINI_DIR/.settings.json.XXXXXX" 2>/dev/null) || tmp=""
  if [ -n "$tmp" ] && "$@" >"$tmp" 2>/dev/null &&
    mv -f -- "$tmp" "$settings" 2>/dev/null &&
    [ -f "$settings" ] && [ ! -L "$settings" ]; then
    return 0
  fi
  if [ -n "$tmp" ]; then
    rm -f -- "$tmp"
    byre_sa_drop_interior "$settings" "$tmp"
  fi
  not_seeding "could not write $settings safely"
}

if command -v jq >/dev/null 2>&1; then
  if [ -L "$settings" ] || { [ -e "$settings" ] && [ ! -f "$settings" ]; }; then
    not_seeding "$settings is a symlink or not a regular file"
  elif [ ! -f "$settings" ]; then
    seed_settings printf '%s\n' '{"security":{"auth":{"selectedType":"oauth-personal"}}}'
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
      seed_settings jq '.security = ((.security // {}) + {auth: ((.security.auth // {}) + {selectedType: "oauth-personal"})})' "$settings"
    elif [ "$state" = "weird" ]; then
      not_seeding "settings.json has an unexpected security/auth shape"
    fi
  fi
fi
exit 0
