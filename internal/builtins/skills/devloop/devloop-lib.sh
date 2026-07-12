#!/bin/sh
# byre devloop shared shell lib — sourced by the devloop firstrun hook and
# byre-codereview, never executed directly. Shipped by the devloop skill to
# /usr/local/lib/byre-devloop-lib.sh.

# byre_devloop_dir <root>: ensure <root>/.devloop exists, is a REAL directory,
# and self-ignores (its .gitignore is "*", so nothing in it is ever committed).
# Hardened against planted nodes: a symlink (tested with -L, no-follow) or any
# non-dir at .devloop, and a symlink/non-regular .gitignore, are removed first
# so writes can't be redirected elsewhere; the self-ignore content is then
# FORCED atomically (temp + rename — never written through an existing node,
# never trusting what's there). Returns nonzero only when the directory itself
# can't be provided; the .gitignore write is best-effort.
byre_devloop_dir() {
  d="$1/.devloop"
  # A non-directory node at .devloop (a user file, or a planted symlink that
  # would redirect our writes) is NOT ours to destroy: warn and stand down —
  # devloop degrades for the session instead of silently deleting it.
  if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then
    echo "byre devloop: $d exists and is not a directory — leaving it alone; devloop's working files (DIARY.md, reviews.md) are unavailable until it is moved" >&2
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
