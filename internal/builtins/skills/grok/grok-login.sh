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
# no-browser sandbox). NOTE: grok access tokens last ~6h and refresh silently;
# --device-auth is only needed again when the chain can no longer renew (or
# after a logout).
command -v grok >/dev/null 2>&1 || exit 0
export GROK_HOME="${GROK_HOME:-/home/dev/.grok-home}"
cred="$GROK_HOME/auth.json"
# A symlinked credential never counts — drop it so a clean re-login writes a
# fresh regular file a planted link can't redirect. This also heals boxes the
# retired grok-shared-auth skill damaged (ADR 0023): its link into the
# identity volume now points at a dead credential, and grok's refresh
# rotation means the shared file can never come back — remove it and log in
# per box. (The ADR 0017 carve-out that kept identity-volume links is gone
# with the retirement.) Runs BEFORE the XAI_API_KEY short-circuit below: a
# stored credential shadows the key, so a dead link left in place would
# override a working key. Removal is announced, not silent — the link may
# be the user's own arrangement, and they should know it stopped working.
if [ -L "$cred" ]; then
  if rm -f "$cred" 2>/dev/null; then
    echo "byre: removed symlinked grok credential (symlinks never count; shared-auth is retired, ADR 0023) — grok logs in per project." >&2
  else
    echo "byre: WARNING — could not remove symlinked grok credential $cred; it shadows any XAI_API_KEY and grok auth will misbehave until it's removed by hand." >&2
  fi
fi
# Shared auth owns login UX when it's enabled: with GROK_AUTH_PROVIDER_COMMAND
# set (the grok-shared-auth broker, ADR 0036), grok gets its credential from
# the broker and a per-box login would just create an orphaned chain — the
# companion's own firstrun hook seeds the SHARED store instead. The symlink
# heal above still runs first: a planted link misbehaves either way.
[ -n "$GROK_AUTH_PROVIDER_COMMAND" ] && exit 0
# A static XAI_API_KEY makes the file login unnecessary (grok uses the key as
# a fallback when no session credential exists — so don't create one).
[ -n "$XAI_API_KEY" ] && exit 0
# Already authenticated? grok has no `login status` probe (unlike codex), so
# the guard is a shape sniff: auth.json is scope-keyed maps of {"key": token}
# (the vendor installer's own parser), so a credential-bearing file contains
# a "key" member. This catches the empty/truncated artifacts an interrupted
# login could leave (which a bare -s test would wrongly count as logged in)
# but not a chain that can no longer refresh — that surfaces at use time
# (headless grok HANGS on a device prompt then; field-observed 2026-07-10),
# where the fix is the same command: `grok login --device-auth`.
[ -s "$cred" ] && grep -q '"key"' "$cred" 2>/dev/null && exit 0

# Clean skip on Ctrl-C: handle SIGINT and exit 0 so we don't propagate a
# signal-death toward the launcher — the box proceeds to the agent regardless.
trap 'echo; echo "byre: grok login skipped. To do it later, open another terminal and run '\''byre shell'\'', then '\''grok login --device-auth'\''."; exit 0' INT

echo ""
echo "=== byre: first-run Grok login ==="
echo "Authorize below; stored per-project, survives rebuilds. Ctrl-C to skip."
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
