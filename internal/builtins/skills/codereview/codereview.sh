#!/usr/bin/env bash
# byre-codereview — an independent second-opinion review of the current changes.
# Shipped by the codereview skill; pairs with a reviewer skill that installs the
# reviewer binary: codex (the default), grok, and/or claude. Reviews the working
# tree's git changes and prints findings, and appends them to
# .byre-devlog/reviews.md.
#
#   byre-codereview                        # review current changes (codex)
#   byre-codereview "focus area"           # focus the review
#   byre-codereview --continue "..."       # re-check after fixes (resumes session)
#   byre-codereview --reviewer grok "..."  # use grok as the reviewer
#   byre-codereview --raw "prompt"         # your prompt verbatim, no review prompt
#
# BYRE_REVIEWER sets the default reviewer (codex when unset).
#
# --raw replaces the built-in review prompt entirely: the arguments become the
# whole prompt (required). The mechanics stay — reviewer enforcement flags,
# session resume, the tripwire, the reviews.md log (tagged "raw") — but the
# execution policy below is only as strong as YOUR prompt, and the truncation
# marker check is skipped since nothing mandates a "Probes run:" section.
#
# Review execution policy (the prompt below enforces it, the tripwire checks
# it): the reviewer may run cheap, targeted, read-only probes to put evidence
# behind a specific finding — a --help, a one-liner repro — but never builds or
# the project's test suite (the author owns green; re-running it buys latency,
# not evidence), and never anything that mutates the tree, git state, or shared
# state. After every run the script re-hashes the working tree and warns loudly
# if it changed (legibility, not a gate).
set -euo pipefail

usage() {
  cat <<'EOF'
byre-codereview — an independent second-opinion review of the current changes.

Usage:
  byre-codereview                        review current changes
  byre-codereview "focus area"           review current changes, focused on a topic
  byre-codereview --continue "..."       re-check after fixes (resumes prior session)
  byre-codereview --reviewer <name> ...  choose the reviewer: codex (default) | grok | claude
  byre-codereview --raw "prompt"         send YOUR prompt verbatim (skips the
                                         built-in review prompt; mechanics stay)
  byre-codereview --raw -- "--anything"  -- ends option parsing, so option-shaped
                                         prompt text passes through

BYRE_REVIEWER sets the default reviewer.
EOF
}

REVIEWER="${BYRE_REVIEWER:-codex}"
CONTINUE=false
RAW=false
FOCUS=()
expect_reviewer=false
ddash=false
for arg in "$@"; do
  if [ "$ddash" = true ]; then
    FOCUS+=("$arg")
    continue
  fi
  if [ "$expect_reviewer" = true ]; then
    REVIEWER="$arg"
    expect_reviewer=false
    continue
  fi
  case "$arg" in
    -h|--help) usage; exit 0 ;;
    --continue) CONTINUE=true ;;
    --raw) RAW=true ;;
    --reviewer) expect_reviewer=true ;;
    --reviewer=*) REVIEWER="${arg#--reviewer=}" ;;
    # Everything after -- is prompt text, never an option — the only way an
    # option-shaped prompt ("--help") can reach the reviewer, raw or focused.
    --) ddash=true ;;
    *) FOCUS+=("$arg") ;;
  esac
done
if [ "$expect_reviewer" = true ]; then
  echo "byre-codereview: --reviewer needs a value (codex | grok | claude)." >&2
  exit 2
fi
if [ "$RAW" = true ] && [ "${#FOCUS[@]}" -eq 0 ]; then
  echo "byre-codereview: --raw needs a prompt (the arguments become the whole prompt)." >&2
  exit 2
fi

case "$REVIEWER" in
  codex|grok|claude) ;;
  *)
    echo "byre-codereview: unsupported reviewer '$REVIEWER' (codex | grok | claude)." >&2
    exit 2
    ;;
esac

if ! command -v "$REVIEWER" >/dev/null 2>&1; then
  echo "byre-codereview: $REVIEWER not found on PATH." >&2
  echo "  Add the $REVIEWER skill (skills = [\"$REVIEWER\", \"codereview\"]) and rebuild." >&2
  for other in codex grok claude; do
    [ "$other" = "$REVIEWER" ] && continue
    if command -v "$other" >/dev/null 2>&1; then
      echo "  ($other is available: byre-codereview --reviewer $other)" >&2
    fi
  done
  exit 127
fi

# Persisted artifacts live in .byre-devlog/ at the repo root — a self-ignoring
# dir (its own .gitignore is "*"), so the review log and agent diary persist via
# the workspace mount but never land in git and need no per-project .gitignore
# entry. byre_devlog_dir (shared lib, shipped alongside this script) provides
# the dir; a user-placed node at that path is never destroyed — the lib warns
# and stands down, which under set -e ends the review here, loudly.
if root=$(git rev-parse --show-toplevel 2>/dev/null); then
  cd "$root"
else
  root="$PWD"
fi
. /usr/local/lib/byre-devlog-lib.sh
byre_devlog_dir "$root"
REVIEW_DIR="$root/.byre-devlog"
LOG_FILE="$REVIEW_DIR/reviews.md"
# Sessions are per-reviewer: resuming a codex thread with grok (or vice versa)
# is meaningless. The codex file keeps its historical name so a box upgraded
# mid-loop can still --continue.
case "$REVIEWER" in
  codex)  SESSION_FILE="$REVIEW_DIR/.review-session" ;;
  grok)   SESSION_FILE="$REVIEW_DIR/.review-session-grok" ;;
  claude) SESSION_FILE="$REVIEW_DIR/.review-session-claude" ;;
esac

# RUN_NOTE annotates the "Running..." line: raw mode says so instead of echoing
# the whole prompt back as a "focus".
if [ "$RAW" = true ]; then RUN_NOTE=" (raw)"; else RUN_NOTE="${FOCUS:+ (focus: ${FOCUS[*]})}"; fi

read -r -d '' PROMPT <<'EOF' || true
You are PURELY a code-review agent: you review, the author fixes. Do not modify
anything — not the working tree, not git state, not credentials or other shared
state. The working tree is re-checked after your run; a reviewer that mutates
the tree contaminates the thing under review.

Do NOT run builds or the project's test suite — the author owns keeping those
green, and re-running them here adds minutes and no evidence. You MAY run
cheap, targeted, read-only probes (a --help, a one-liner repro, inspecting a
generated artifact) when a specific finding you are about to report depends on
a fact you can verify in seconds. If verifying a claim would be expensive or
have side effects, report the finding anyway with its confidence marked down
and say what would verify it.

Process:
1. Read any project guidance you can find (CLAUDE.md / AGENTS.md / README) for context.
2. Run: git status, git diff, git diff --cached, git log --oneline -8.
3. Review the changes (committed-but-recent and uncommitted).

Focus on: correctness bugs and logic errors, missing edge cases, security issues,
and clear code-quality problems. Prefer a short list of high-confidence findings
over a long list of nits. For each finding give file:line, what's wrong, why,
and whether you verified it. End the report with a "Probes run:" list of any
commands you executed beyond the git reads above ("none" if none). Give the
full report as your final message.
EOF

if [ "$RAW" = true ]; then
  # --raw: the arguments ARE the prompt. The enforcement flags and tripwire
  # still apply; the policy the built-in prompt encodes does not.
  PROMPT="${FOCUS[*]}"
elif [ "${#FOCUS[@]}" -gt 0 ]; then
  PROMPT="$PROMPT

Pay particular attention to: ${FOCUS[*]}"
fi

OUT=$(mktemp "$REVIEW_DIR/.out.XXXXXX")
DBG=$(mktemp "$REVIEW_DIR/.dbg.XXXXXX")
cleanup() { rm -f "$OUT" "$DBG"; }

# Snapshot of the working tree the reviewer must not change: status + tracked-
# content diff + untracked-file CONTENT hashes (porcelain alone only lists
# untracked NAMES, so a content-only edit to an existing untracked file would
# slip through; ls-files -o is plumbing, so it also sidesteps a
# status.showUntrackedFiles=no config). Gitignored files (including .byre-devlog/,
# where this script's own log and temp files live) are deliberately outside
# the snapshot. Empty outside git — the tripwire is inert there, matching the
# rest of the script's non-repo degradation.
tree_state() {
  {
    git status --porcelain=v1 2>/dev/null
    git diff HEAD 2>/dev/null
    git ls-files -o --exclude-standard -z 2>/dev/null | sort -z | xargs -0r sha256sum 2>/dev/null
  } | sha256sum 2>/dev/null || true
}
# Fail open but SAY so: without sha256sum every snapshot is empty and the
# tripwire can't fire. All supported bases ship coreutils, so this is a
# one-line legibility note, not machinery.
command -v sha256sum >/dev/null 2>&1 \
  || echo "byre-codereview: note — sha256sum missing, the tree tripwire is disabled." >&2
PRE_STATE=$(tree_state)
# The observe-don't-mutate tripwire. A warning, not a rollback: byre's job is
# to make the violation legible, the human decides what to do with it. Fires
# on any tree change during the run — including a concurrent session's edits —
# so it names both possibilities. Installed as an EXIT trap so it also runs on
# the FAILURE paths: a run that mutates the tree and then dies is exactly the
# contamination case this exists for.
check_tripwire() {
  [ "$(tree_state)" = "$PRE_STATE" ] && return 0
  {
    echo ""
    echo "byre-codereview: WARNING — the working tree changed during this review."
    echo "  Either the reviewer modified files (it must not) or something edited the"
    echo "  tree concurrently. Inspect 'git status' / 'git diff' before trusting or"
    echo "  acting on these findings."
  } >&2
}
trap check_tripwire EXIT

# Append the captured findings to the review log with a timestamp + reviewer.
# A run that died mid-review can leave a plausible-looking fragment (grok's
# permission/sandbox deaths print a preamble, then stop — one got recorded as
# a clean review before this check). The prompt mandates a trailing
# "Probes run:" section, so its absence marks a likely truncation: record it,
# but say so — in the log heading and on stderr. Warn-only: a reviewer that
# merely forgot the section must not have its findings suppressed.
record_review() {
  [ -s "$OUT" ] || return 0
  # Tail-anchored, not body-wide: a review that QUOTES the mandate mid-body
  # (any review of this script would) and then dies must still be flagged —
  # the marker only counts as the trailing section it was mandated to be.
  # Raw runs skip the check entirely: only the built-in prompt mandates the
  # section, so its absence marks nothing.
  note=""
  if [ "$RAW" != true ] && ! tail -n 40 "$OUT" 2>/dev/null | grep -qi 'probes run'; then
    note=" — POSSIBLY TRUNCATED: missing the mandated 'Probes run:' section"
    {
      echo ""
      echo "byre-codereview: WARNING — the review lacks its mandated 'Probes run:' section,"
      echo "  so the run may have died mid-review. Treat the findings — and especially the"
      echo "  APPARENT ABSENCE of findings — accordingly."
    } >&2
  fi
  raw_tag=""
  [ "$RAW" = true ] && raw_tag=", raw"
  { printf '\n## %s (%s%s)%s\n\n' "$(date -u +%FT%TZ)" "$REVIEWER" "$raw_tag" "$note"; cat "$OUT"; } >> "$LOG_FILE"
}

extract_codex_session() {
  grep -m1 '"type":"thread.started"' "$DBG" 2>/dev/null \
    | jq -r '.thread_id' 2>/dev/null || true
}

run_fresh_codex() {
  # Starting fresh: drop any prior session up front, so an interrupted run can't
  # leave a stale session that a later --continue would wrongly resume.
  rm -f "$SESSION_FILE"
  echo "Running code review (codex)${RUN_NOTE} — this may take several minutes..."
  # --sandbox read-only: OS-level enforcement on top of the prompt — codex can
  # read the repo and exec read-only probes, but writes are blocked, so a
  # prompt-injection in the code under review can't act. A probe that needs to
  # write fails inside the sandbox; the prompt tells the reviewer to downgrade
  # the claim's confidence instead.
  # --skip-git-repo-check: codex refuses non-git dirs by default, but half of
  # what byre boxes isn't a repo, and the BOX is the trust boundary here — the
  # check duplicates an enclosure byre already provides (footgun doctrine).
  if codex exec --skip-git-repo-check --json --sandbox read-only "$PROMPT" \
       --output-last-message "$OUT" < /dev/null > "$DBG" 2>&1; then
    sid=$(extract_codex_session)
    [ -n "$sid" ] && [ "$sid" != "null" ] && echo "$sid" > "$SESSION_FILE" || rm -f "$SESSION_FILE"
    cat "$OUT"; record_review; cleanup
  else
    report_failure_codex
    rm -f "$OUT" "$SESSION_FILE"; exit 1
  fi
}

# report_failure_codex inspects the debug log and prints an actionable message.
# The common, opaque failure is an expired/invalidated codex credential: codex
# 401s ("token_expired" / "refresh token ... already used" / "sign in again")
# and the only signal would otherwise be a raw temp log. Codex auth is a
# rotating token, so this WILL recur — name the fix instead of making the next
# person cat a log.
report_failure_codex() {
  if grep -qiE 'token_expired|refresh token|sign in again|authentication token is expired|401 unauthorized' "$DBG" 2>/dev/null; then
    echo "byre-codereview: codex authentication failed — the login expired or was invalidated." >&2
    echo "  Re-authenticate in another terminal: run 'byre shell', then the device-code flow:" >&2
    echo "      codex login --device-auth" >&2
    echo "  (Plain 'codex login' opens a browser flow the box can't complete.)" >&2
    echo "  Debug log: $DBG" >&2
  else
    echo "byre-codereview: review failed. Debug log: $DBG" >&2
  fi
}

run_resume_codex() {
  local sid="$1"
  echo "Continuing previous review session (codex) — this may take several minutes..."
  # The resume subcommand rejects --sandbox ("unexpected argument", clap exit 2
  # — every resume then fell back to a fresh review, silently), but it takes -c
  # overrides, and sandbox_mode is the same knob by its config name. It DOES
  # accept --output-last-message, so the fresh path's extraction works here too.
  if codex exec resume --skip-git-repo-check --json -c sandbox_mode="read-only" \
       "$sid" "$PROMPT" --output-last-message "$OUT" < /dev/null > "$DBG" 2>&1; then
    new=$(extract_codex_session); [ -n "$new" ] && [ "$new" != "null" ] && echo "$new" > "$SESSION_FILE"
    if [ -s "$OUT" ]; then cat "$OUT"; record_review; else echo "(could not extract final message; raw: $DBG)"; fi
    cleanup
  else
    echo "Resume failed — falling back to a fresh review." >&2
    rm -f "$SESSION_FILE"; run_fresh_codex
  fi
}

# Grok reviewer notes. Honest enforcement ordering: the box boundary and the
# tripwire are what actually hold; the tool strip is best-effort narrowing.
# - NO --sandbox: grok's Landlock profiles break tool execution inside a byre
#   box — every tool-using turn returns exit 0 with EMPTY output (verified
#   in-box 2026-07-09, grok 0.2.93 on a linuxkit kernel; nothing in the debug
#   log). If a future grok fixes sandboxing in containers, --sandbox read-only
#   here would restore codex-parity (write-block + child-network-block).
# - --disallowed-tools strips the file-edit + todo tools (write_file and
#   apply_patch are speculative IDs — unknown names are accepted harmlessly,
#   verified). bash stays: the review needs git reads and cheap probes, so
#   free-form writes remain POSSIBLE — that's what the tripwire is for.
# - GROK_SUBAGENTS=0 closes the subagent bypass (a spawned task would get the
#   FULL toolset, edit tools included). It must be the env var: putting
#   "Agent" in the denylist breaks grok session construction outright
#   (0.2.93 run_terminal_cmd params bug, verified in-box).
# - --always-approve is REQUIRED for headless tool use: grok's default
#   permission mode prompts for any command off its safe fast-path list (git
#   reads, ls/cat/grep — NOT rg, bash, or --help probes), headless has no TTY
#   to prompt, and the turn silently DIES there — exit 0, preamble-only
#   output (reproduced in-box 2026-07-09; it bit a fresh box first, whose
#   stub got recorded as a clean review). My earlier runs only worked because
#   that box's config carried permission_mode = "always-approve". The user
#   guide's recommended headless pattern (--permission-mode dontAsk + narrow
#   --allow rules) fits byre's deny-by-default posture better, but 0.2.93
#   does not enforce dontAsk from the flag (documented wiring note) —
#   tighten to that when it ships. Risk is bounded the same way as the rest:
#   edit tools stripped, subagents off, tripwire, box boundary.
# - Because of the silent-empty failure shape above, empty output on exit 0 is
#   treated as a FAILED run, never recorded as a clean review. Session-
#   construction errors are ALSO exit 0 — but with the error text on stdout
#   (verified) — so those are caught by shape too. Mid-run deaths that leave a
#   preamble are caught by the "Probes run:" marker check in record_review.
GROK_TOOL_STRIP="search_replace,todo_write,write_file,apply_patch"

# The observed 0.2.93 exit-0 startup failure: "Couldn't create session: ..."
# printed to stdout. Never record that as a review. Anchored to the START of
# the FIRST line: a legitimate review may well QUOTE the phrase (a review of
# this very file would), and grepping the whole body would discard it.
grok_startup_error() { head -n1 "$OUT" 2>/dev/null | grep -q "^Couldn.t create session"; }

run_fresh_grok() {
  rm -f "$SESSION_FILE"
  echo "Running code review (grok)${RUN_NOTE} — this may take several minutes..."
  # -s pre-assigns the session UUID (grok creates it), so --continue can
  # --resume it later without parsing any output.
  local sid; sid=$(cat /proc/sys/kernel/random/uuid)
  if GROK_SUBAGENTS=0 grok -p "$PROMPT" -s "$sid" --always-approve --disallowed-tools "$GROK_TOOL_STRIP" \
       < /dev/null > "$OUT" 2> "$DBG"; then
    if [ ! -s "$OUT" ] || grok_startup_error; then
      echo "byre-codereview: grok failed before reviewing (exit 0 with empty output, or a startup error):" >&2
      cat "$OUT" >&2
      echo "  Debug log: $DBG" >&2
      rm -f "$OUT" "$SESSION_FILE"; exit 1
    fi
    echo "$sid" > "$SESSION_FILE"
    cat "$OUT"; record_review; cleanup
  else
    # Surface whatever partial output exists — same courtesy as the startup
    # path; failure details otherwise vanish with the temp file.
    [ -s "$OUT" ] && cat "$OUT" >&2
    report_failure_grok
    rm -f "$OUT" "$SESSION_FILE"; exit 1
  fi
}

run_resume_grok() {
  local sid="$1"
  echo "Continuing previous review session (grok) — this may take several minutes..."
  if GROK_SUBAGENTS=0 grok -p "$PROMPT" --resume "$sid" --always-approve --disallowed-tools "$GROK_TOOL_STRIP" \
       < /dev/null > "$OUT" 2> "$DBG" && [ -s "$OUT" ] && ! grok_startup_error; then
    cat "$OUT"; record_review; cleanup
  else
    # Same partial-output courtesy as the fresh path before the fallback eats it.
    [ -s "$OUT" ] && cat "$OUT" >&2
    echo "Resume failed — falling back to a fresh review." >&2
    rm -f "$SESSION_FILE"; run_fresh_grok
  fi
}

# Claude reviewer notes (all claims verified in-box 2026-07-10).
# - INDEPENDENCE CAVEAT: when claude is also the box's authoring agent, this is
#   a second PASS by the same model family, not a second opinion. Prefer codex
#   or grok when they're available; claude earns its keep as the reviewer in a
#   box where it's the only CLI, or as a differently-prompted extra pass.
# - Enforcement, same honest ordering as grok: the box boundary and the
#   tripwire are what actually hold; the tool strip is best-effort narrowing.
#   --disallowedTools strips the file-edit tools plus Task (a spawned subagent
#   would get the full toolset — the same bypass grok closes via
#   GROK_SUBAGENTS=0). --allowedTools Bash keeps git reads and cheap probes
#   working (headless runs auto-DENY any tool that would prompt — a deny, not
#   grok's silent death), so free-form writes remain POSSIBLE — that's what
#   the tripwire is for. No codex-style OS sandbox is applied.
# - --safe-mode keeps the REVIEWED repo's claude customizations (settings,
#   hooks, plugins, MCP servers, CLAUDE.md) from loading: without it a
#   malicious repo's hooks would execute at reviewer startup — code running
#   BEFORE the prompt or denylist gets a say. The review prompt is
#   self-contained, so the reviewer loses nothing it needs.
# - The PROMPT rides stdin: --allowedTools/--disallowedTools are variadic and
#   swallow a trailing prompt argument (each prompt word became a bogus
#   permission rule when passed after them).
# - Sessions: --session-id pre-assigns the UUID, like grok's -s; --resume works
#   headless, repeatedly, against the SAME id. A run that dies early can still
#   consume its pre-assigned id ("already in use"), which is one more reason
#   every fresh run mints a new one.
CLAUDE_TOOL_STRIP="Edit,Write,NotebookEdit,TodoWrite,Task"

run_fresh_claude() {
  rm -f "$SESSION_FILE"
  echo "Running code review (claude)${RUN_NOTE} — this may take several minutes..."
  local sid; sid=$(cat /proc/sys/kernel/random/uuid)
  if printf '%s' "$PROMPT" | claude -p --safe-mode --session-id "$sid" \
       --allowedTools "Bash" --disallowedTools "$CLAUDE_TOOL_STRIP" \
       > "$OUT" 2> "$DBG"; then
    # Exit 0 with nothing to say has no legitimate reading — never record it
    # as a clean review (grok's lesson, applied preemptively).
    if [ ! -s "$OUT" ]; then
      echo "byre-codereview: claude produced no output despite exit 0." >&2
      echo "  Debug log: $DBG" >&2
      rm -f "$OUT" "$SESSION_FILE"; exit 1
    fi
    echo "$sid" > "$SESSION_FILE"
    cat "$OUT"; record_review; cleanup
  else
    # Surface whatever partial output exists — claude prints some failures
    # (e.g. "Not logged in") to STDOUT, and they'd otherwise vanish with the
    # temp file.
    [ -s "$OUT" ] && cat "$OUT" >&2
    report_failure_claude
    rm -f "$OUT" "$SESSION_FILE"; exit 1
  fi
}

run_resume_claude() {
  local sid="$1"
  echo "Continuing previous review session (claude) — this may take several minutes..."
  if printf '%s' "$PROMPT" | claude -p --safe-mode --resume "$sid" \
       --allowedTools "Bash" --disallowedTools "$CLAUDE_TOOL_STRIP" \
       > "$OUT" 2> "$DBG" && [ -s "$OUT" ]; then
    cat "$OUT"; record_review; cleanup
  else
    # Same partial-output courtesy as the fresh path before the fallback eats it.
    [ -s "$OUT" ] && cat "$OUT" >&2
    echo "Resume failed — falling back to a fresh review." >&2
    rm -f "$SESSION_FILE"; run_fresh_claude
  fi
}

# "Not logged in · Please run /login" arrives on STDOUT with exit 1 (verified),
# so the auth grep covers $OUT as well as the debug log. Tight patterns only,
# same rationale as grok's.
report_failure_claude() {
  if grep -qiE 'not logged in|please run /login|oauth token.*(expired|revoked)|invalid api key|401' "$OUT" "$DBG" 2>/dev/null; then
    echo "byre-codereview: claude authentication failed." >&2
    echo "  Log in once in the box (run 'claude', then /login). If this box rides a" >&2
    echo "  shared token (claude-shared-auth), see that skill's notes instead." >&2
    echo "  Debug log: $DBG" >&2
  else
    echo "byre-codereview: review failed. Debug log: $DBG" >&2
  fi
}

# Tight auth patterns only — bare "expired"/"authentication" match too many
# ordinary error lines and would send people into a pointless re-login loop.
report_failure_grok() {
  if grep -qiE 'token_expired|refresh token|not logged in|sign in|401|invalid api key' "$DBG" 2>/dev/null; then
    echo "byre-codereview: grok may need re-authentication (its ~6h tokens refresh silently until the chain dies)." >&2
    echo "  Run 'byre shell', then: grok login --device-auth" >&2
    echo "  Debug log: $DBG" >&2
  else
    echo "byre-codereview: review failed. Debug log: $DBG" >&2
  fi
}

if [ "$CONTINUE" = true ] && [ -f "$SESSION_FILE" ]; then
  sid=$(tr '[:upper:]' '[:lower:]' < "$SESSION_FILE")
  if [[ "$sid" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]; then
    "run_resume_$REVIEWER" "$sid"
  else
    rm -f "$SESSION_FILE"; "run_fresh_$REVIEWER"
  fi
else
  "run_fresh_$REVIEWER"
fi
