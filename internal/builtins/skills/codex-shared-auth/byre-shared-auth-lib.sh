# byre shared-auth library — the filesystem primitives the shared-auth and
# login hooks work through. Sourced, never executed; five skills ship this
# file byte-identical to /usr/local/lib/byre-shared-auth-lib.sh (ADR 0056:
# identical staged bytes compose, anything else refuses the assemble).
#
# THIS COPY IS THE CANONICAL ONE (internal/builtins/byre-shared-auth-lib.sh).
# Each shipping skill dir holds a copy of it, pinned byte-identical by
# TestSharedAuthLibCopiesAreIdentical; edit here and copy out.
#
# Every function takes its paths as positional arguments, keeps no state and
# prints nothing: the hook owns the vocabulary its user reads, and the policy
# around an outcome (which login wins, whether a lock is held, whether one
# bad file stops the rest) stays in the hook. Return codes are the interface.
#
# POSIX sh, so the opencode login hook can source it too.

# True when PATH is the path it is spelled as: not a symlink, and its
# physical path equal to its spelling. Never a lexical prefix test -- that is
# the shape ..-traversals pass.
byre_sa_path_as_spelled() {
  [ ! -L "$1" ] && [ "$(cd -- "$1" 2>/dev/null && pwd -P)" = "$1" ]
}

# True when DIR can be created and written through without a symlink
# carrying the writes elsewhere: DIR is not a symlink, and the nearest
# EXISTING ancestor is the path it is spelled as. Asked BEFORE mkdir -p, so
# nothing is created on the far side of a planted link; ask
# byre_sa_path_as_spelled on DIR itself afterwards, for a link that raced in
# during the mkdir.
byre_sa_identity_route_safe() {
  local existing="$1"
  while [ ! -e "$existing" ] && [ ! -L "$existing" ] && [ "$existing" != / ]; do
    existing=$(dirname -- "$existing")
  done
  [ ! -L "$1" ] && byre_sa_path_as_spelled "$existing"
}

# True when PATH holds something a symlink may not point at: a symlink (an
# in-place credential write would chain through it to a file of the planter's
# choosing) or any non-regular object. Absent is fine -- a dangling link is
# the expected first-login state. -L is tested first: -e/-f follow links.
byre_sa_shared_unsafe() {
  [ -L "$1" ] || { [ -e "$1" ] && [ ! -f "$1" ]; }
}

# Prints PATH's symlink target. Fails when PATH is not a symlink, or when the
# target holds a newline: $(readlink) strips trailing newlines, so a target
# of "<path><newline>" -- a different file -- would compare equal to
# "<path>". The sentinel x is appended inside the substitution and stripped
# after, so every other byte survives.
byre_sa_link_target() {
  [ -L "$1" ] || return 1
  local target nl='
'
  target=$(readlink -n -- "$1" 2>/dev/null && printf x) || return 1
  target=${target%x}
  case "$target" in
  *"$nl"*) return 1 ;;
  esac
  printf '%s' "$target"
}

# True when PATH is a symlink whose target is exactly TARGET.
byre_sa_links_to() {
  local target
  target=$(byre_sa_link_target "$1") || return 1
  [ "$target" = "$2" ]
}

# Removes the leftover a refused claim left INSIDE DEST, when DEST turns out
# to be a directory -- a real one, or a SYMLINK to one, which `mv` follows: it
# judges its destination by stat, which is exactly why GNU has -T and why BSD
# mv, having none, cannot be told otherwise. Without -T, `ln` and `mv` put
# their source INSIDE a directory destination, so by the time the checks reject
# the claim the file already exists as DEST/<temp name>: for a promote or a
# publish that is this box's login, mode 600, on the machine-scoped identity
# volume its sibling boxes read -- or, through a planted link, in whichever
# directory on that volume the planter chose -- while the hook reports the
# login stayed local.
#
# This is the ONE place these hooks resolve a planted symlink on purpose, and
# it is safe for a reason that holds nowhere else: the only path component byre
# supplies is its own fresh mktemp basename, made moments ago, so the removal
# can unlink nothing except the file byre just deposited. A planter who
# pre-created that exact random name inside their own target directory loses
# their own file and nothing besides. Every other check here REFUSES rather
# than resolving, because every other path is one the planter chose.
byre_sa_drop_interior() {
  if [ -d "$1" ]; then
    rm -f -- "$1/${2##*/}" 2>/dev/null
  fi
  return 0
}

# Points LOCAL at SHARED as a symlink, keeping any login LOCAL holds if it
# cannot. Returns 0 when LOCAL IS that link (including when it already was), 2
# when SHARED is not something a link may point at, 3 when LOCAL is an object
# this may not replace (a FIFO, socket or real directory: not a login byre
# made, and rm -f cannot remove a directory anyway), and 1 when the link could
# not be made.
#
# The link is built under a temp name in LOCAL's own directory and renamed over
# LOCAL, so LOCAL is replaced in ONE step: a failure anywhere leaves a regular
# file at LOCAL exactly as it was. The one thing a failure can leave changed is
# an old LINK at LOCAL that resolved to a directory -- that one is dropped
# before the rename (see below), and it was a link, never a login; its target
# is untouched. SHARED is re-vetted just before the rename: it passed the check
# above and could have been swapped since.
#
# No `mv -T` (GNU-only; the macOS CI leg runs these hooks with BSD mv). A
# plain mv onto a directory moves the link INTO it, so: a real directory at
# LOCAL takes the 3 above, an old link that resolves to a directory is dropped
# first, and the rename counts only once LOCAL is checked to BE the link, which
# closes a directory raced in between.
byre_sa_assert_link() {
  byre_sa_shared_unsafe "$2" && return 2
  byre_sa_links_to "$1" "$2" && return 0
  if [ -e "$1" ] && [ ! -L "$1" ] && [ ! -f "$1" ]; then
    return 3
  fi
  local tmp ok=""
  tmp=$(mktemp "$(dirname -- "$1")/.$(basename -- "$1").XXXXXX" 2>/dev/null) || return 1
  if rm -f -- "$tmp" 2>/dev/null && ln -s -- "$2" "$tmp" 2>/dev/null; then
    if byre_sa_shared_unsafe "$2"; then
      rm -f -- "$tmp" 2>/dev/null
      return 2
    fi
    if [ -L "$1" ] && [ -d "$1" ] && ! rm -f -- "$1" 2>/dev/null; then
      :
    elif mv -f -- "$tmp" "$1" 2>/dev/null && byre_sa_links_to "$1" "$2"; then
      ok=1
    fi
  fi
  if [ -z "$ok" ]; then
    rm -f -- "$tmp" 2>/dev/null
    byre_sa_drop_interior "$1" "$tmp"
    return 1
  fi
  return 0
}

# Claims SHARED as a copy of LOCAL, exclusively. Returns 0 when this box's
# copy IS SHARED now, 2 when another object is there (the race, lost), and 1
# when the claim could not be made. LOCAL is never removed here: the caller's
# assert replaces it with the link in one step, so a failed assert still
# leaves this box a login.
#
# A temp copy in SHARED's own directory, then a hard `ln -n` onto SHARED,
# which fails with EEXIST if another box won. Two boxes both finding SHARED
# absent would both pass a `[ ! -e ]` test, and mv across volumes is
# copy+unlink, so the second would silently replace the first box's login.
# No `ln -T` (GNU-only; BSD ln on the macOS CI leg): a directory that
# appeared at SHARED would take the link INSIDE it, so the claim counts only
# once SHARED is checked to BE the temp file -- same inode (-ef) and not a
# symlink, and the name the link left inside that directory is then removed
# (byre_sa_drop_interior, which says why it must be). -n keeps a raced-in
# symlink-to-directory from being followed (GNU and BSD ln both take it).
# And the mode goes AFTER the `--`: BSD chmod's
# getopt stops at the mode operand, so a `--` written after the mode is read
# as a filename and the command fails -- a Linux run cannot catch it, since
# GNU chmod permutes.
byre_sa_promote() {
  local tmp
  tmp=$(mktemp "$(dirname -- "$2")/.$(basename -- "$2").XXXXXX" 2>/dev/null) || return 1
  if cp -- "$1" "$tmp" 2>/dev/null && chmod -- 600 "$tmp" 2>/dev/null &&
    ln -n -- "$tmp" "$2" 2>/dev/null && [ ! -L "$2" ] && [ "$tmp" -ef "$2" ]; then
    rm -f -- "$tmp" 2>/dev/null
    return 0
  fi
  rm -f -- "$tmp" 2>/dev/null
  byre_sa_drop_interior "$2" "$tmp"
  { [ -e "$2" ] || [ -L "$2" ]; } && return 2
  return 1
}
