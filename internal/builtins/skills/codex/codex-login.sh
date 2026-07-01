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
# fresh regular file a planted link can't redirect.
[ -L "$cred" ] && rm -f "$cred"
codex login status >/dev/null 2>&1 && exit 0

# Clean skip on Ctrl-C: handle SIGINT and exit 0 so we don't propagate a
# signal-death toward the launcher — the box proceeds to the agent regardless.
trap 'echo; echo "byre: codex login skipped — run codex login --device-auth later."; exit 0' INT

echo ""
echo "=== byre: first-run Codex login (for byre-codereview) ==="
echo "Authorize below; stored per-project, survives rebuilds. Ctrl-C to skip."
echo ""
# Bound the wait so a stale/unused device code can't hold the box open for long
# (codex polls until you authorize); on timeout/failure we fall through to launch.
# --foreground keeps codex in the terminal's foreground process group so a Ctrl-C
# reaches it immediately (without it, timeout runs the child in its own group and
# the interrupt wouldn't land until the timeout elapsed).
TO=""
command -v timeout >/dev/null 2>&1 && TO="timeout --foreground 600"
$TO codex login --device-auth \
  || echo "byre: codex login didn't complete — run 'codex login --device-auth' later." >&2
exit 0
