#!/bin/sh
# byre devlog shared shell lib — sourced by the devlog firstrun hook and
# byre-codereview, never executed directly. Shipped to
# /usr/local/lib/byre-devlog-lib.sh by BOTH the devlog and codereview skills
# (identical copies, pinned by a builtins test), so each skill works without
# the other.

# byre_devlog_dir <root>: ensure <root>/.byre-devlog exists, is a REAL
# directory, and self-ignores (its .gitignore is "*", so nothing in it is ever
# committed). The dir was born as .devloop/; the rename dropped that name
# outright — an old dir is never read, moved, or deleted (rename it by hand to
# keep its history). Nothing user-placed is ever destroyed: a symlink (tested
# with -L, no-follow) or any non-dir at .byre-devlog is NOT ours to remove —
# warn and stand down for the session instead. Inside a directory we do own,
# the self-ignore marker is byre's own file: a planted symlink there is
# unlinked (one level — its target is never touched) so writes can't be
# redirected elsewhere, any other non-regular node stands down too, and the
# content is then FORCED atomically (temp + rename — never written through an
# existing node, never trusting what's there). Returns 0 only when the dir
# exists AND self-ignores: a caller that writes artifacts into a non-ignoring
# dir would strand them as committable untracked files (and byre-codereview's
# tree tripwire would fire on its own temp files), so every degraded outcome
# is warn + nonzero — callers that can shrug (the firstrun hook) mask it.
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
  # The marker itself: a symlink is removed (one level, never its target —
  # left in place, mv would treat a link-to-directory as a destination dir
  # and write THROUGH it); any other non-regular node (a user-made directory,
  # say) is not ours to destroy — say so and leave the dir un-self-ignored.
  if [ -L "$gi" ]; then
    rm -f "$gi"
  elif [ -e "$gi" ] && [ ! -f "$gi" ]; then
    echo "byre devlog: $gi exists and is not a regular file — leaving it alone; $d is NOT self-ignoring until it is moved" >&2
    return 1
  fi
  tmp="$d/.gitignore.tmp.$$"
  rm -rf "$tmp"
  if ! { printf '*\n' > "$tmp" && mv -f "$tmp" "$gi"; }; then
    rm -f "$tmp"
    echo "byre devlog: could not write $gi — $d is NOT self-ignoring" >&2
    return 1
  fi
  return 0
}
