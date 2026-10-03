#!/bin/sh
# opencode first-run auth hook — runs as the dev user, before the agent
# launches, on a fresh box. If no opencode credential is stored, trigger
# `opencode auth login` so the agent works out of the box. Mirrors the
# codex/grok login hooks.
#
# `opencode auth login` is an interactive multi-provider picker (runs in the
# terminal, no in-box browser needed): API keys for most providers, and for
# Anthropic a "Login with Claude Pro/Max" paste-code OAuth flow. The
# credential lands in the .opencode state volume, so this runs once per
# project (and survives rebuilds). Best-effort: skip with Ctrl-C (or if it
# fails/times out) and the box still launches — re-auth later with
# `opencode auth login` from `byre shell`.
command -v opencode >/dev/null 2>&1 || exit 0
# opencode's data dir is the XDG data home (VERIFIED, 1.18.2: `opencode
# debug paths`); honoring XDG_DATA_HOME here keeps the hook faithful to the
# CLI's own resolution (and is the test seam).
data_root="${XDG_DATA_HOME:-/home/dev/.local/share}"
cred="$data_root/opencode/auth.json"
# The filesystem primitives this hook judges a planted credential with (the
# newline-safe link read, the absent-or-regular target test) live in one
# library five skills ship to this path. BYRE_SHARED_AUTH_LIB is a test seam,
# not a user knob: byre refuses BYRE_* names in a project [env]
# (internal/config/config.go). Without the library this hook cannot judge a
# symlinked credential at all, so it says so and offers no login rather than
# running one through whatever is there.
BYRE_SA_LIB="${BYRE_SHARED_AUTH_LIB:-/usr/local/lib/byre-shared-auth-lib.sh}"
if [ ! -r "$BYRE_SA_LIB" ]; then
  echo "byre: cannot read the shared-auth library ($BYRE_SA_LIB); not offering the opencode login" >&2
  exit 0
fi
. "$BYRE_SA_LIB"
# A symlinked credential must never count — drop it so a clean re-login
# writes a fresh regular file a planted link can't redirect. ONE exception:
# opencode-shared-auth's own link into ITS identity dir is legitimate, and
# a DANGLING one is its expected first-login state (opencode writes in
# place, VERIFIED through a symlink 2026-07-16 — the login writes through
# it into the shared volume). The trusted dir is HARDCODED and compared by
# EQUALITY, deliberately: an env-derived base would let a config-supplied
# [env] var redefine the trusted namespace (firstrun hooks inherit the
# container env — the codex hook hardcodes for the same reason), and a
# broader .byre-identity/* match would trust links into SIBLING agents'
# identity dirs — through which a login here would overwrite that agent's
# machine-wide credential with opencode's incompatible store. Canonicalize
# the target's PARENT dir (the final auth.json may be absent); a lexical
# prefix check would accept planted ..-traversals and reject legitimate
# relative links. Relative targets resolve from the link's own directory.
# And the resolved target itself must be absent (dangling: first login) or
# a regular non-symlink file: a link planted AT the identity dir's auth.json
# would chain the login's write onward, and opencode-shared-auth refuses
# that case without touching this link -- so it is dropped here instead,
# and the login writes a safe local regular file.
shared_auth=""
if [ -L "$cred" ]; then
  # A relative target may start with "-", so every path-taking tool here
  # takes `--`. The library's read refuses a target holding a newline, which
  # leaves tdir empty and takes the removal path below.
  target=""
  tdir=""
  if target=$(byre_sa_link_target "$cred"); then
    tdir="$(cd -- "$data_root/opencode" 2>/dev/null && cd -- "$(dirname -- "$target")" 2>/dev/null && pwd -P)" || tdir=""
  fi
  tfile="/home/dev/.byre-identity/opencode/auth.json"
  # Full-path equality: the OWN identity dir AND the auth.json basename
  # (opencode-shared-auth links exactly that file) -- a dir-only match would
  # trust a link to any OTHER name inside the dir -- AND the target object
  # absent or regular. One conjunction, so no half can be dropped alone.
  if [ "$tdir" = "/home/dev/.byre-identity/opencode" ] && [ "$(basename -- "$target")" = "auth.json" ] && ! byre_sa_shared_unsafe "$tfile"; then
    shared_auth=1
  else
    # A failed removal (an unwritable data dir) must not fall through: the
    # rejected link would still be there, and a login whose target is
    # writable would write THROUGH it. Stop without offering the login.
    if ! rm -f -- "$cred"; then
      echo "byre: could not remove the symlinked opencode credential $cred; not offering the login -- remove it in byre shell" >&2
      exit 0
    fi
  fi
fi
# Anything else that is not a regular file (a FIFO, socket, directory) is
# not a credential opencode wrote. Stop BEFORE grep/tail/opencode open it:
# opening a FIFO blocks. The -s test below happens to keep the sniff off an
# empty FIFO, but the login then ran against it, and opencode's in-place
# write blocks on a FIFO nobody reads -- with no tty guard here, a headless
# launch sat in that write until the timeout. Never delete an unknown
# object -- say so.
if [ -e "$cred" ] && [ ! -L "$cred" ] && [ ! -f "$cred" ]; then
  echo "byre: opencode credential path $cred is not a regular file; not reading it and not offering the login -- inspect it in byre shell" >&2
  exit 0
fi
# A static provider key in the environment makes the file login unnecessary
# for byre's expected pairings (Anthropic API billing, or OpenCode Zen).
# That includes a `byre credentials` value: the launcher exports delivered
# credentials ABOVE the firstrun loop (internal/gen/launcher.sh), so this
# child process inherits them. OpenCode is multi-provider — other provider
# env keys exist too; anyone riding one can just Ctrl-C the prompt below once.
[ -n "$ANTHROPIC_API_KEY" ] && exit 0
[ -n "$OPENCODE_API_KEY" ] && exit 0
# Already authenticated? opencode has no `login status` probe, so the guard
# is a shape sniff (the grok precedent): auth.json is a provider-keyed map
# whose entries all carry a "type" member ({"type":"api","key":...} or
# {"type":"oauth",...} — the binary's own Auth schema), and a complete
# JSON.stringify'd store ends in "}". The trailing-brace check catches the
# truncated artifact an interrupted IN-PLACE write could leave EVEN when
# the truncation point falls past a "type" token (opencode writes with no
# temp+rename, so partial files are real); an empty store ({}) fails the
# "type" test. Still not caught: a server-side-expired credential — that
# surfaces at use time, where the fix is the same command:
# `opencode auth login`.
if [ -s "$cred" ] && grep -q -- '"type"' "$cred" 2>/dev/null \
  && [ "$(tail -c 1 "$cred" 2>/dev/null)" = "}" ]; then
  exit 0
fi

# Clean skip on Ctrl-C: handle SIGINT and exit 0 so we don't propagate a
# signal-death toward the launcher — the box proceeds to the agent regardless.
trap 'echo; echo "byre: opencode login skipped. To do it later, open another terminal and run '\''byre shell'\'', then '\''opencode auth login'\''."; exit 0' INT

echo ""
echo "=== byre: first-run OpenCode login ==="
if [ -n "$shared_auth" ]; then
  echo "Pick a provider below; stored machine-wide (shared-auth: all your byre projects). Ctrl-C to skip."
else
  echo "Pick a provider below; stored per-project, survives rebuilds. Ctrl-C to skip."
fi
echo ""
# Bound the wait so an abandoned picker can't hold the box open for long; on
# timeout/failure we fall through to launch. --foreground keeps opencode in
# the terminal's foreground process group so a Ctrl-C reaches it immediately
# (without it, timeout runs the child in its own group and the interrupt
# wouldn't land until the timeout elapsed).
TO=""
command -v timeout >/dev/null 2>&1 && TO="timeout --foreground 600"
$TO opencode auth login \
  || echo "byre: opencode login didn't complete. To do it later, open another terminal and run 'byre shell', then 'opencode auth login'." >&2
exit 0
