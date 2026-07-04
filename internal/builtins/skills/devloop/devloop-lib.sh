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
  if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then rm -rf "$d"; fi
  mkdir -p "$d" || return 1
  gi="$d/.gitignore"
  if [ -L "$gi" ] || { [ -e "$gi" ] && [ ! -f "$gi" ]; }; then rm -rf "$gi"; fi
  tmp="$d/.gitignore.tmp.$$"
  rm -rf "$tmp"
  { printf '*\n' > "$tmp" && mv -f "$tmp" "$gi"; } || rm -f "$tmp"
  return 0
}
