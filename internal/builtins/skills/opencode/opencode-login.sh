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
shared_auth=""
if [ -L "$cred" ]; then
  target="$(readlink "$cred")"
  tdir="$(cd "$data_root/opencode" 2>/dev/null && cd "$(dirname "$target")" 2>/dev/null && pwd -P)" || tdir=""
  if [ "$tdir" = "/home/dev/.byre-identity/opencode" ]; then
    shared_auth=1
  else
    rm -f "$cred"
  fi
fi
# A static provider key in the environment makes the file login unnecessary
# for byre's expected pairings (Anthropic API billing, or OpenCode Zen).
# OpenCode is multi-provider — other provider env keys exist too; anyone
# riding one can just Ctrl-C the prompt below once.
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
if [ -s "$cred" ] && grep -q '"type"' "$cred" 2>/dev/null \
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
