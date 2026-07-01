#!/usr/bin/env bash
# byre-codereview — an independent, Codex-backed review of the current changes.
# Shipped by the devloop skill; pairs with the codex skill (which installs the
# codex binary). Reviews the working tree's git changes and prints findings, and
# appends them to .devloop/reviews.md.
#
#   byre-codereview                       # review current changes
#   byre-codereview "focus area"          # focus the review
#   byre-codereview --continue "..."      # re-check after fixes (resumes session)
set -euo pipefail

if ! command -v codex >/dev/null 2>&1; then
  echo "byre-codereview: codex not found on PATH." >&2
  echo "  Add the codex skill (skills = [\"codex\", \"devloop\"]) and rebuild." >&2
  exit 127
fi

# Persisted artifacts live in .devloop/ at the repo root — a self-ignoring dir
# (its own .gitignore is "*"), so the review log and agent diary persist via the
# workspace mount but never land in git and need no per-project .gitignore entry.
if root=$(git rev-parse --show-toplevel 2>/dev/null); then
  cd "$root"
else
  root="$PWD"
fi
REVIEW_DIR="$root/.devloop"
# Remove a SYMLINK (-L, no-follow) or non-dir at .devloop, and a symlink/non-
# regular .gitignore, so writes can't be redirected through a planted node; then
# force the self-ignore content atomically (temp + rename, never write through it).
if [ -L "$REVIEW_DIR" ] || { [ -e "$REVIEW_DIR" ] && [ ! -d "$REVIEW_DIR" ]; }; then rm -rf "$REVIEW_DIR"; fi
mkdir -p "$REVIEW_DIR"
GI="$REVIEW_DIR/.gitignore"
if [ -L "$GI" ] || { [ -e "$GI" ] && [ ! -f "$GI" ]; }; then rm -rf "$GI"; fi
GITMP="$REVIEW_DIR/.gitignore.tmp.$$"
rm -rf "$GITMP"
printf '*\n' > "$GITMP" && mv -f "$GITMP" "$GI" || rm -f "$GITMP"
SESSION_FILE="$REVIEW_DIR/.review-session"
LOG_FILE="$REVIEW_DIR/reviews.md"

CONTINUE=false
FOCUS=()
for arg in "$@"; do
  case "$arg" in
    --continue) CONTINUE=true ;;
    *) FOCUS+=("$arg") ;;
  esac
done

read -r -d '' PROMPT <<'EOF' || true
You are PURELY a code-review agent. You run in a read-only sandbox: you cannot
modify files, run tests, or run builds — only read and reason. Do not attempt to.

Process:
1. Read any project guidance you can find (CLAUDE.md / AGENTS.md / README) for context.
2. Run: git status, git diff, git diff --cached, git log --oneline -8.
3. Review the changes (committed-but-recent and uncommitted).

Focus on: correctness bugs and logic errors, missing edge cases, security issues,
and clear code-quality problems. Prefer a short list of high-confidence findings
over a long list of nits. For each finding give file:line, what's wrong, and why.
Give the full report as your final message.
EOF

if [ "${#FOCUS[@]}" -gt 0 ]; then
  PROMPT="$PROMPT

Pay particular attention to: ${FOCUS[*]}"
fi

OUT=$(mktemp "$REVIEW_DIR/.out.XXXXXX")
DBG=$(mktemp "$REVIEW_DIR/.dbg.XXXXXX")
cleanup() { rm -f "$OUT" "$DBG"; }

extract_session() {
  grep -m1 '"type":"thread.started"' "$DBG" 2>/dev/null \
    | jq -r '.thread_id' 2>/dev/null || true
}

# Append the captured findings to the review log with a timestamp.
record_review() {
  [ -s "$OUT" ] || return 0
  { printf '\n## %s\n\n' "$(date -u +%FT%TZ)"; cat "$OUT"; } >> "$LOG_FILE"
}

run_fresh() {
  # Starting fresh: drop any prior session up front, so an interrupted run can't
  # leave a stale session that a later --continue would wrongly resume.
  rm -f "$SESSION_FILE"
  echo "Running code review${FOCUS:+ (focus: ${FOCUS[*]})} — this may take several minutes..."
  # read-only sandbox: codex can read the repo and run git (reads) but cannot
  # modify anything — so a prompt-injection in the code under review can't act.
  if codex exec --json --sandbox read-only "$PROMPT" \
       --output-last-message "$OUT" < /dev/null > "$DBG" 2>&1; then
    sid=$(extract_session)
    [ -n "$sid" ] && [ "$sid" != "null" ] && echo "$sid" > "$SESSION_FILE" || rm -f "$SESSION_FILE"
    cat "$OUT"; record_review; cleanup
  else
    report_failure
    rm -f "$OUT" "$SESSION_FILE"; exit 1
  fi
}

# report_failure inspects the debug log and prints an actionable message. The
# common, opaque failure is an expired/invalidated codex credential: codex 401s
# ("token_expired" / "refresh token ... already used" / "sign in again") and the
# only signal would otherwise be a raw temp log. Codex auth is a rotating token,
# so this WILL recur — name the fix instead of making the next person cat a log.
report_failure() {
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

run_resume() {
  local sid="$1"
  echo "Continuing previous review session — this may take several minutes..."
  if codex exec resume --json --sandbox read-only \
       "$sid" "$PROMPT" < /dev/null > "$DBG" 2>&1; then
    new=$(extract_session); [ -n "$new" ] && [ "$new" != "null" ] && echo "$new" > "$SESSION_FILE"
    grep '"type":"item.completed"' "$DBG" \
      | grep -E '"type":"(agent_message|assistant_message)"' | tail -1 \
      | jq -r '.item.text // .item.output_text // (.item.content[]?.text // empty)' > "$OUT" 2>/dev/null || true
    if [ -s "$OUT" ]; then cat "$OUT"; record_review; else echo "(could not extract final message; raw: $DBG)"; fi
    cleanup
  else
    echo "Resume failed — falling back to a fresh review." >&2
    rm -f "$SESSION_FILE"; run_fresh
  fi
}

if [ "$CONTINUE" = true ] && [ -f "$SESSION_FILE" ]; then
  sid=$(tr '[:upper:]' '[:lower:]' < "$SESSION_FILE")
  if [[ "$sid" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]; then
    run_resume "$sid"
  else
    rm -f "$SESSION_FILE"; run_fresh
  fi
else
  run_fresh
fi
