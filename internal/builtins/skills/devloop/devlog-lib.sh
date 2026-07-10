#!/bin/sh
# byre devlog shared shell lib — sourced by the devloop firstrun hook and
# byre-codereview, never executed directly. Shipped to
# /usr/local/lib/byre-devlog-lib.sh.

# byre_devlog_dir <root>: ensure <root>/.byre-devlog exists, is a REAL
# directory, and self-ignores (its .gitignore is "*", so nothing in it is ever
# committed). The dir was born as .devloop/; an old-name dir left by a
# pre-rename box is migrated into place first, so diaries and review logs
# carry over. Hardened against planted nodes: a symlink (tested with -L,
# no-follow) or any non-dir at .byre-devlog, and a symlink/non-regular
# .gitignore, are removed first so writes can't be redirected elsewhere; the
# self-ignore content is then FORCED atomically (temp + rename — never written
# through an existing node, never trusting what's there). Returns nonzero only
# when the directory itself can't be provided; the .gitignore write is
# best-effort.
byre_devlog_dir() {
  d="$1/.byre-devlog"
  # Old-name migration: only a REAL old dir moves (a planted symlink is left
  # where it is — never followed, never deleted), and never onto an existing
  # new-name node, which the hardening below owns instead. Best-effort: a
  # failed mv just means a fresh dir.
  old="$1/.devloop"
  if [ ! -e "$d" ] && [ ! -L "$d" ] && [ -d "$old" ] && [ ! -L "$old" ]; then
    mv "$old" "$d" 2>/dev/null || true
  fi
  if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then rm -rf "$d"; fi
  mkdir -p "$d" || return 1
  gi="$d/.gitignore"
  if [ -L "$gi" ] || { [ -e "$gi" ] && [ ! -f "$gi" ]; }; then rm -rf "$gi"; fi
  tmp="$d/.gitignore.tmp.$$"
  rm -rf "$tmp"
  { printf '*\n' > "$tmp" && mv -f "$tmp" "$gi"; } || rm -f "$tmp"
  return 0
}
