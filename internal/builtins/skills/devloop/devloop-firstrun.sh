#!/bin/sh
# devloop firstrun hook — runs (as root, each launch) before the agent starts.
# Ensures the project's .devloop/ scratch dir exists and is self-ignoring: its
# own .gitignore is "*", so the agent diary and review log persist via the
# workspace mount but never land in git, with no per-project .gitignore entry.
# Idempotent; best-effort (must never block launch). Done as the dev user so the
# files aren't root-owned.
ws=/workspace
[ -d "$ws" ] || exit 0
gosu "${BYRE_USER:-1000:1000}" sh -c '
  d="$1/.devloop"
  # Remove a SYMLINK (tested with -L, which does not follow) or any non-directory
  # at .devloop, so writes land in a real dir we own and cannot be redirected
  # elsewhere by a node a prior agent run may have planted.
  if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then rm -rf "$d"; fi
  mkdir -p "$d" || exit 0
  gi="$d/.gitignore"
  if [ -L "$gi" ] || { [ -e "$gi" ] && [ ! -f "$gi" ]; }; then rm -rf "$gi"; fi
  # Write atomically (temp + rename): never write THROUGH a planted symlink, and
  # FORCE the self-ignore content rather than trusting whatever is there.
  tmp="$d/.gitignore.tmp.$$"
  rm -rf "$tmp"
  printf "*\n" > "$tmp" && mv -f "$tmp" "$gi" || rm -f "$tmp"
' _ "$ws" 2>/dev/null || true
