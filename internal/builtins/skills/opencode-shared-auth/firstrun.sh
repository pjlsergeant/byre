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
#
# No `set -e`: every step that can fail is checked by hand or best-effort,
# and the launch proceeds regardless (exit 0 on every path).
IDENTITY_DIR="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/opencode"
SHARED="$IDENTITY_DIR/auth.json"
DATA_DIR="${XDG_DATA_HOME:-/home/dev/.local/share}/opencode"
cred="$DATA_DIR/auth.json"

# The filesystem primitives (identity route, shared-path vetting, the link
# assert, the exclusive promote) live in one library five skills ship to this
# path; this hook owns the policy and the messages. BYRE_SHARED_AUTH_LIB is a
# test seam, not a user knob: byre refuses BYRE_* names in a project [env]
# (internal/config/config.go). A library byre cannot read means none of the
# hardening below is in force, which is never something to do quietly.
BYRE_SA_LIB="${BYRE_SHARED_AUTH_LIB:-/usr/local/lib/byre-shared-auth-lib.sh}"
if [ ! -r "$BYRE_SA_LIB" ]; then
  echo "byre opencode-shared-auth: cannot read the shared-auth library ($BYRE_SA_LIB) — shared auth not asserted this launch." >&2
  exit 0
fi
. "$BYRE_SA_LIB"

# One refusal for the shared path, said from three places below.
refuse_shared() {
  echo "byre opencode-shared-auth: refusing: the shared credential path ($SHARED) is a symlink or not a regular file — shared auth not asserted this launch; remove it in byre shell." >&2
  exit 0
}

# A symlinked identity DIR -- or any symlinked ancestor -- is not ours:
# linking auth.json through it would send every login on this machine
# wherever the planted link points. Refuse, and leave any per-project login
# untouched.
if ! byre_sa_identity_route_safe "$IDENTITY_DIR"; then
  echo "byre opencode-shared-auth: refusing: the identity dir ($IDENTITY_DIR) is or resolves through a symlink — shared auth not asserted this launch." >&2
  exit 0
fi

# Failing to create either dir means shared auth cannot be asserted this
# launch; say so before degrading (best-effort, never block the launch) —
# otherwise the fallback to a per-project login is silent and the user
# believes the machine-wide credential is in play.
if ! mkdir -p -- "$IDENTITY_DIR" "$DATA_DIR" 2>/dev/null; then
  echo "byre opencode-shared-auth: cannot create $IDENTITY_DIR or $DATA_DIR — shared auth not asserted this launch (falling back to a per-project login)." >&2
  exit 0
fi

# Re-checked on the dir itself now it exists: a link raced in during mkdir -p
# carries the shared credential off the same way.
if ! byre_sa_path_as_spelled "$IDENTITY_DIR"; then
  echo "byre opencode-shared-auth: refusing: the identity dir ($IDENTITY_DIR) resolves through a symlink — shared auth not asserted this launch." >&2
  exit 0
fi

# The shared credential itself must be a regular file (or absent): a symlink
# planted AT $SHARED would chain the data-dir link to a file of the planter's
# choosing, and opencode's in-place write follows the chain. Refuse BEFORE
# the promote/assert steps, leaving the data-dir auth.json untouched
# (whatever it is, it is no worse than before this launch).
byre_sa_shared_unsafe "$SHARED" && refuse_shared

# Adopt an existing per-project login rather than clobbering it: a NON-EMPTY
# real auth.json with no shared copy yet becomes the machine-wide credential.
# (An empty one is no login to announce, and the assert below replaces it
# with the link like any other local file.) The claim is exclusive, so two
# boxes launching at once cannot overwrite each other; a failed claim must
# NOT fall through to the assert, which would discard the only login and link
# to nothing. Messages print only once the outcome is known — a "promoting"
# line before the claim would be false for the box that loses.
promoted=""
lost=""
if [ -f "$cred" ] && [ ! -L "$cred" ] && [ -s "$cred" ] && [ ! -e "$SHARED" ]; then
  byre_sa_promote "$cred" "$SHARED"
  case $? in
  0)
    promoted=1
    # Say it out loud: this box's login is now THE machine credential.
    echo "byre opencode-shared-auth: promoted this box's existing OpenCode login to the machine-wide shared credential" >&2
    ;;
  2)
    # Lost the race: shared wins, as the heal policy below says. Re-check the
    # winner's object (the refusal above ran before it existed). The
    # "replaced" message waits until the assert has actually replaced it.
    byre_sa_shared_unsafe "$SHARED" && refuse_shared
    lost=1
    ;;
  *)
    echo "byre opencode-shared-auth: could not promote this box's OpenCode login into the identity volume; keeping the per-project login — shared auth not asserted this launch." >&2
    exit 0
    ;;
  esac
fi

# Assert the symlink. This also heals the logout-fork: `opencode auth
# logout` rewrites/removes entries in the local file, and a later login
# would otherwise write a local file, silently forking off the shared
# credential. When both a local file AND a shared credential exist, the
# shared one wins (the local copy is a fork; discarding it is the healing).
#
# A just-promoted local file is the shared credential's own copy: replacing
# it discards nothing, so no "replaced" message for it.
discard=""
[ -z "$promoted" ] && [ -f "$cred" ] && [ ! -L "$cred" ] && [ -e "$SHARED" ] && discard=1
byre_sa_assert_link "$cred" "$SHARED"
case $? in
0) ;;
2) refuse_shared ;;
3)
  echo "byre opencode-shared-auth: refusing: $cred is not a regular file — shared auth not asserted this launch; inspect it in byre shell." >&2
  exit 0
  ;;
*)
  echo "byre opencode-shared-auth: could not assert the shared-auth link; this box keeps its local credential — shared auth not asserted this launch." >&2
  exit 0
  ;;
esac
# Say it out loud when a local login was replaced (lost race, or a fork
# discarded for the shared login); a won promotion already said its piece.
if [ -n "$lost" ]; then
  echo "byre opencode-shared-auth: another box promoted its OpenCode login first; this box's local login was replaced by the shared one" >&2
elif [ -n "$discard" ]; then
  echo "byre opencode-shared-auth: replaced this box's local OpenCode login with the machine-wide shared credential (the local copy was a post-logout fork)" >&2
fi
[ -f "$SHARED" ] && [ ! -L "$SHARED" ] && chmod -- 600 "$SHARED" 2>/dev/null || true

# API-key logins only share safely. The file is shared WHOLE, so an OAuth
# entry (Claude Pro/Max, Copilot, ...) in it IS shared too — and it rotates
# a single-use refresh token that concurrent boxes race on and
# cascade-logout. API keys are static and share cleanly. If the shared store
# holds an OAuth entry, say so — friendly, and NEVER touch it: it's a live
# working credential, and quarantining it would be the grok-v1
# heal-that-clobbers mistake. auth.json is written with
# JSON.stringify(...,2), so entries read `"type": "oauth"`; tolerate spacing.
if [ -f "$SHARED" ] && [ ! -L "$SHARED" ] && grep -Eq -- '"type"[[:space:]]*:[[:space:]]*"oauth"' "$SHARED" 2>/dev/null; then
  echo "byre opencode-shared-auth: the shared auth.json is shared whole, and API-key logins only share safely: it holds an OAuth login (e.g. Claude Pro/Max), which is shared too and races when multiple boxes refresh it — log in with an API key for that provider instead." >&2
fi
exit 0
