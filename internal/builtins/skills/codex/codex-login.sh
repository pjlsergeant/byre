#!/bin/sh
# codex first-run auth hook — runs as the dev user, before the agent launches, on
# a fresh box. If the codex credential is missing, trigger the device-auth login so
# byre-codereview (and any codex use) works out of the box. Mirrors how moarcode
# did it in its entrypoint.
#
# --device-auth prints a URL + code to authorize in your browser — no in-box
# browser needed. The credential lands in the .codex state volume, so this runs
# once per project (and survives rebuilds). Best-effort: skip with Ctrl-C (or if
# it fails/times out) and the box still launches — re-auth later with
# `codex login --device-auth` (NOT plain `codex login`, which needs a browser the
# box doesn't have). Codex creds are a rotating token and are NOT seedable, so
# device-auth is the only way back in.
command -v codex >/dev/null 2>&1 || exit 0
export CODEX_HOME="${CODEX_HOME:-/home/dev/.codex-home}"
# Already authenticated? Ask codex itself (`codex login status`) rather than
# testing the file. "auth.json is non-empty" can't tell a usable credential from
# a corrupt/partial one (an interrupted prior login), so file-presence wrongly
# skips a needed re-auth. NOTE: this catches missing/corrupt creds, not a token
# the server has since expired or invalidated — that only surfaces at use time,
# where byre-codereview reports it and points back to `codex login --device-auth`.
cred="$CODEX_HOME/auth.json"
# A symlinked credential must never count — drop it so a clean re-login writes a
# fresh regular file a planted link can't redirect. ONE exception (ADR 0017):
# codex-shared-auth's own link into the identity volume is legitimate, and a
# DANGLING one is its expected first-login state (the login below writes
# through it into the shared volume) — it must be kept, never removed. The
# narrowing is accepted: the agent can already read the credential the link
# would redirect.
shared_auth=""
if [ -L "$cred" ]; then
  # Canonicalize the target's PARENT dir (the final auth.json may be absent --
  # dangling is the expected first-login state); a lexical prefix check would
  # accept planted ..-traversals and reject legitimate relative links.
  # Relative targets resolve from the link's own directory.
  target="$(readlink "$cred")"
  tdir="$(cd "$CODEX_HOME" 2>/dev/null && cd "$(dirname "$target")" 2>/dev/null && pwd -P)" || tdir=""
  # EQUALITY against the FULL canonical target — codex's OWN identity dir AND
  # the auth.json basename (codex-shared-auth links exactly that file) — not a
  # /home/dev/.byre-identity/* wildcard: a broader match would trust a link
  # into a SIBLING agent's identity dir, through which a `codex login` would
  # overwrite that agent's machine-wide credential with codex's incompatible
  # store; and a dir-only match would trust a link to any OTHER name inside
  # codex's dir. Mirrors the opencode-login hook.
  if [ "$tdir" = "/home/dev/.byre-identity/codex" ] && [ "$(basename "$target")" = "auth.json" ]; then
    shared_auth=1
  else
    rm -f "$cred"
  fi
fi
codex login status >/dev/null 2>&1 && exit 0

# Clean skip on Ctrl-C: handle SIGINT and exit 0 so we don't propagate a
# signal-death toward the launcher — the box proceeds to the agent regardless.
trap 'echo; echo "byre: codex login skipped. To do it later, open another terminal and run '\''byre shell'\'', then '\''codex login --device-auth'\''."; exit 0' INT

echo ""
echo "=== byre: first-run Codex login (for byre-codereview) ==="
if [ -n "$shared_auth" ]; then
  echo "Authorize below; stored machine-wide (shared-auth: all your byre projects). Ctrl-C to skip."
else
  echo "Authorize below; stored per-project, survives rebuilds. Ctrl-C to skip."
fi
echo ""
# Bound the wait so a stale/unused device code can't hold the box open for long
# (codex polls until you authorize); on timeout/failure we fall through to launch.
# --foreground keeps codex in the terminal's foreground process group so a Ctrl-C
# reaches it immediately (without it, timeout runs the child in its own group and
# the interrupt wouldn't land until the timeout elapsed).
TO=""
command -v timeout >/dev/null 2>&1 && TO="timeout --foreground 600"
$TO codex login --device-auth \
  || echo "byre: codex login didn't complete. To do it later, open another terminal and run 'byre shell', then 'codex login --device-auth'." >&2
exit 0
