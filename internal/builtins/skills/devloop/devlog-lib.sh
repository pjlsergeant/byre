#!/bin/sh
# byre devlog shared shell lib — sourced by the devloop firstrun hook and
# byre-codereview, never executed directly. Shipped to
# /usr/local/lib/byre-devlog-lib.sh by BOTH the devloop and codereview skills
# (identical copies, pinned by a builtins test), so each skill works without
# the other.

# byre_devlog_dir <root>: ensure <root>/.byre-devlog exists, is a REAL
# directory, and self-ignores (its .gitignore is "*", so nothing in it is ever
# committed). The dir was born as .devloop/; the rename dropped that name
# outright — an old dir is never read, moved, or deleted (rename it by hand to
# keep its history). Nothing user-placed is ever destroyed: a symlink (tested
# with -L, no-follow) or any non-dir at .byre-devlog is NOT ours to remove —
# warn and stand down for the session instead. Inside a directory we do own,
# the self-ignore marker is byre's own file: a symlink/non-regular .gitignore
# is removed so writes can't be redirected elsewhere, and the content is then
# FORCED atomically (temp + rename — never written through an existing node,
# never trusting what's there). Returns nonzero only when the directory itself
# can't be provided; the .gitignore write is best-effort.
byre_devlog_dir() {
  d="$1/.byre-devlog"
  # A non-directory node at .byre-devlog (a user file, or a planted symlink
  # that would redirect our writes) is NOT ours to destroy: warn and stand
  # down — the devlog degrades for the session instead of silently deleting it.
  if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then
    echo "byre devlog: $d exists and is not a directory — leaving it alone; the devlog's working files (DIARY.md, reviews.md) are unavailable until it is moved" >&2
    return 1
  fi
  mkdir -p "$d" || return 1
  gi="$d/.gitignore"
  if [ -L "$gi" ] || { [ -e "$gi" ] && [ ! -f "$gi" ]; }; then rm -rf "$gi"; fi
  tmp="$d/.gitignore.tmp.$$"
  rm -rf "$tmp"
  { printf '*\n' > "$tmp" && mv -f "$tmp" "$gi"; } || rm -f "$tmp"
  return 0
}
