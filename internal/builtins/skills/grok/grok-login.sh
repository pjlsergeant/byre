#!/bin/sh
# grok first-run auth hook — runs as the dev user, before the agent launches, on
# a fresh box. If the grok credential is missing, trigger the device-auth login
# so grok works out of the box. Mirrors the codex-login hook.
#
# --device-auth is grok's documented headless flow: it prints a URL + one-time
# code to authorize in your browser — no in-box browser needed. The credential
# lands in the .grok state volume, so this runs once per project (and survives
# rebuilds). Best-effort: skip with Ctrl-C (or if it fails/times out) and the
# box still launches — re-auth later with `grok login --device-auth` (plain
# `grok login` starts a browser-redirect flow that cannot complete in a
# no-browser sandbox). NOTE: grok credentials expire after ~7 days, so this
# command is also the routine re-auth path, not just first-run.
command -v grok >/dev/null 2>&1 || exit 0
export GROK_HOME="${GROK_HOME:-/home/dev/.grok-home}"
# A static XAI_API_KEY takes precedence over the file credential — with one
# set, the file login is unnecessary.
[ -n "$XAI_API_KEY" ] && exit 0
cred="$GROK_HOME/auth.json"
# A symlinked credential must never count — drop it so a clean re-login writes a
# fresh regular file a planted link can't redirect. ONE exception (ADR 0017):
# grok-shared-auth's own link into the identity volume is legitimate, and a
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
  tdir="$(cd "$GROK_HOME" 2>/dev/null && cd "$(dirname "$target")" 2>/dev/null && pwd -P)" || tdir=""
  case "$tdir/" in
  /home/dev/.byre-identity/*) shared_auth=1 ;;
  *) rm -f "$cred" ;;
  esac
fi
# Already authenticated? grok has no `login status` probe (unlike codex), so
# the guard is a shape sniff: auth.json is scope-keyed maps of {"key": token}
# (the vendor installer's own parser), so a credential-bearing file contains
# a "key" member. This catches the empty/truncated artifacts an interrupted
# login could leave (which a bare -s test would wrongly count as logged in)
# but not an EXPIRED token — grok tokens last ~7 days — which surfaces at use
# time, where the fix is the same command: `grok login --device-auth`.
[ -s "$cred" ] && grep -q '"key"' "$cred" 2>/dev/null && exit 0

# Clean skip on Ctrl-C: handle SIGINT and exit 0 so we don't propagate a
# signal-death toward the launcher — the box proceeds to the agent regardless.
trap 'echo; echo "byre: grok login skipped. To do it later, open another terminal and run '\''byre shell'\'', then '\''grok login --device-auth'\''."; exit 0' INT

echo ""
echo "=== byre: first-run Grok login ==="
if [ -n "$shared_auth" ]; then
  echo "Authorize below; stored machine-wide (shared-auth: all your byre projects). Ctrl-C to skip."
else
  echo "Authorize below; stored per-project, survives rebuilds. Ctrl-C to skip."
fi
echo ""
# Bound the wait so a stale/unused device code can't hold the box open for long;
# on timeout/failure we fall through to launch. --foreground keeps grok in the
# terminal's foreground process group so a Ctrl-C reaches it immediately
# (without it, timeout runs the child in its own group and the interrupt
# wouldn't land until the timeout elapsed).
TO=""
command -v timeout >/dev/null 2>&1 && TO="timeout --foreground 600"
$TO grok login --device-auth \
  || echo "byre: grok login didn't complete. To do it later, open another terminal and run 'byre shell', then 'grok login --device-auth'." >&2
exit 0
