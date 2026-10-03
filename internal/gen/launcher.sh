#!/usr/bin/env bash
# byre launcher — the constant ENTRYPOINT.
#
# Runs UNPRIVILEGED as the in-box 'dev' user (the Dockerfile bakes that user to
# the host UID/GID and sets USER dev, so PID 1 here is already the runtime user).
# There is no root phase and no gosu drop: /home/dev and the named volumes are
# born owned by the baked UID at build time, so nothing needs re-owning. The
# launcher just places git identity, exports the per-session context var,
# exports delivered credentials, runs first-run hooks, and execs the agent —
# all as the same user. Agent context is INJECTED by the agent command (ADR
# 0046); the launcher writes no agent file.
set -euo pipefail

# The dev user's home is baked at build time (skills.DevHome); not an env
# knob — the chassis paths are constants, not configuration.
export HOME=/home/dev

# Launch gate — a network-posture skill (e.g. firewall) bakes a gate file whose
# content is a loopback port. When present, byre applies the skill's network
# setup from OUTSIDE the box (a netns-init helper container) after start, and
# that helper listens on the port once the rules are applied and verified. We
# poll-connect until it does, and only then proceed — so NOTHING in the box
# (context placement, the credential wait, first-run hooks, the agent) runs
# before the wall is up.
# Every failure path fails CLOSED: no listener within the timeout means the
# box exits instead of launching open. The handshake is deliberately stateless
# (no marker file): a `docker restart` recreates the netns without the rules,
# and this gate then simply times out again rather than trusting stale state.
# The env overrides exist for byre's own tests; a user setting them is
# disabling their own protection, which is theirs to do (footgun doctrine).
GATE_FILE="${BYRE_LAUNCH_GATE_FILE:-/etc/byre/launch-gate}"
if [ -s "$GATE_FILE" ]; then
  gate_port="$(tr -cd '0-9' < "$GATE_FILE")"
  gate_timeout="${BYRE_LAUNCH_GATE_TIMEOUT:-30}"
  gate_ok=
  SECONDS=0
  while [ "$SECONDS" -lt "$gate_timeout" ]; do
    # Bash's /dev/tcp: a successful connect means the netns-init helper is
    # listening, which it only does after its rules are applied and verified.
    if (exec 3<>"/dev/tcp/127.0.0.1/$gate_port") 2>/dev/null; then
      gate_ok=1
      break
    fi
    sleep 0.2
  done
  if [ -z "$gate_ok" ]; then
    echo "byre: launch gate: network setup never signaled ready on 127.0.0.1:${gate_port:-?} after ${gate_timeout}s — refusing to launch without it (failing closed)." >&2
    echo "byre: (running this image without byre? the firewall netns helper must run alongside it — \`byre ejectfirewall\` prints it. To launch with NO walls instead: set BYRE_LAUNCH_GATE_FILE=/dev/null.)" >&2
    exit 1
  fi
fi

# git identity: mark the workspace safe so git doesn't refuse the bind-mounted
# repo (owned by the same uid, but git's dubious-ownership check is path-based).
WS="${BYRE_WORKSPACE_DIR:-/workspace}"
git config --global --add safe.directory "$WS" >/dev/null 2>&1 || true

# Worktree populate: `byre worktree` registers the worktree --no-checkout in a
# one-shot creation container (runner.WorktreeAdd — every mutating git
# operation on the repo runs in a box, never on the host; ADR 0009), which
# drops a marker in the worktree git dir. The actual checkout runs HERE, in
# the box, where the repo's git extensions — the post-checkout hook,
# smudge/process filters — run contained like all its other code. Gated on the marker,
# so a normal box start (no marker) does nothing; the marker clears ONLY on a
# successful checkout, so a failed populate stays resumable on the next develop
# and never traps the user out of the box. Best-effort: a populate failure warns
# and still launches (an empty tree the user can fix beats no box).
#
# Detection needs NO git binary: a linked worktree's .git is a file
# "gitdir: <path>", and that path is bind-mounted into the box at its host path
# (same-path mounting, ADR 0009). Only the checkout itself needs git — so a box
# without git gets a loud, actionable message instead of a silently empty tree.
if [ -f "$WS/.git" ]; then
  wt_gitdir="$(sed -n 's/^gitdir: //p' "$WS/.git" 2>/dev/null | head -n1)"
  if [ -n "$wt_gitdir" ] && [ -f "$wt_gitdir/byre-needs-checkout" ]; then
    if ! command -v git >/dev/null 2>&1; then
      echo "byre: this worktree still needs to be checked out here, but the box has no git." >&2
      echo "byre: add 'git' to the box (byre config → Packages), then re-run 'byre develop' here." >&2
    elif git -C "$WS" checkout >&2; then
      echo "byre: populated the worktree checkout inside the box." >&2
      rm -f "$wt_gitdir/byre-needs-checkout"
    else
      echo "byre: could not fully populate the worktree checkout — the working tree may be empty or incomplete." >&2
      echo "byre: fix the cause and run 'git checkout' in the box, or re-run 'byre develop' here to retry." >&2
    fi
  elif [ -z "$(ls -A "$WS" 2>/dev/null | grep -v '^\.git$')" ]; then
    # A linked worktree with nothing but .git and NO pending marker: either a
    # marker a concurrent box deleted (a hint, not a source of truth; ADR 0009) or
    # a checkout that never happened. Surface it loudly rather than launch
    # silently into an empty tree. Not a block: the user may want the box.
    echo "byre: this worktree looks unpopulated and byre has no pending-checkout record for it." >&2
    echo "byre: if that's unexpected, run 'git checkout' here, or re-create it with 'byre worktree'." >&2
  fi
fi

# Per-session agent context — the DYNAMIC additions only: the egress
# allowlist this box actually enforces, and the --self-edit note when that
# grant is present. Exported as BYRE_SESSION_CONTEXT for the agent command
# to inject alongside the BAKED /etc/byre/agent-context.md (claude: the
# byre-claude-launch wrapper merges baked file + this var into one
# --append-system-prompt-file). byre never writes an agent-owned file to deliver prose (ADR
# 0046): the agent's memory file belongs to the user, and expropriating it
# was never byre's to do.
# Always exported (possibly empty), so an injecting command's
# "$BYRE_SESSION_CONTEXT" reference is safe unconditionally. Best-effort
# throughout: a failure composing informational text must never block the
# launch.
CTX_DIR="${BYRE_CONTEXT_DIR:-/etc/byre}"
BYRE_SESSION_CONTEXT=""
append_session_ctx() {
  [ -n "$1" ] || return 0
  if [ -n "$BYRE_SESSION_CONTEXT" ]; then
    BYRE_SESSION_CONTEXT="${BYRE_SESSION_CONTEXT}

$1"
  else
    BYRE_SESSION_CONTEXT="$1"
  fi
}
# Egress announcement: the wall is up (a posture skill baked the gate we
# already waited on) AND byre handed us the enforced allowlist — the same
# BYRE_EGRESS string the netns helper applied, so what we announce IS what
# is enforced. Informational only, hence an env var is fine here: a user
# setting it lies to their own agent (footgun doctrine).
if [ -s "$GATE_FILE" ] && [ -n "${BYRE_EGRESS+set}" ]; then
  if [ -n "$BYRE_EGRESS" ]; then
    eg_list="$(printf "%s" "$BYRE_EGRESS" | sed "s/ /, /g" 2>/dev/null || true)"
    append_session_ctx "## This session's egress allowlist

${eg_list}

Anything not listed is closed. The list was resolved when this session
started; a restart re-reads the config."
  else
    append_session_ctx "## This session's egress allowlist

The allowlist is EMPTY: every outbound connection is closed. A restart
re-reads the config."
  fi
fi
# self-edit grant = the store actually bind-mounted READ-WRITE at
# /home/dev/.byre-self (what --self-edit does). Check /proc/mounts for an rw
# mount at that target — not mere file existence (a baked files/ entry) nor a
# read-only bind. (Deliberately rw-mounting something else at byre'"'"'s own
# internal self-edit path is a self-granted, status-visible choice; the note
# is only informational either way.)
if grep -Eq " /home/dev/\.byre-self [^ ]+ rw[, ]" /proc/mounts 2>/dev/null && [ -f "$CTX_DIR/self-edit.md" ]; then
  append_session_ctx "$(cat "$CTX_DIR/self-edit.md" 2>/dev/null || true)"
fi
# Exported with a LEADING blank line when non-empty, so an adapter can
# concatenate baked+session directly ("$(cat agent-context.md)$BYRE_SESSION_CONTEXT")
# without separator logic of its own.
if [ -n "$BYRE_SESSION_CONTEXT" ]; then
  BYRE_SESSION_CONTEXT="

$BYRE_SESSION_CONTEXT"
fi
export BYRE_SESSION_CONTEXT

# Credential export — the launcher's end of credential delivery.
# BYRE_CRED_EXPECT is set at create time ONLY when this launch decrypted a
# deliverable set and scheduled an inject; it is purely a wait/export
# protocol flag ("wait bounded for .done, then export from the manifest") —
# no verification meaning. The wait is bounded and fails CLOSED, exactly like
# the launch gate above: a config that declares credentials describes a box
# whose agent needs them, and launching one that quietly lacks them produces
# failures nobody can read.
#
# That is also the restart refusal, and for the same reason the gate's is:
# the handshake is stateless, the tmpfs empties on restart, so a restarted,
# un-re-unlocked box simply times out here and exits instead of running with
# credentials it no longer has.
#
# PLACEMENT. byre_credentials_apply is called TWICE, and this is the only
# place either call is explained. First below the launch gate (ADR 0011:
# nothing runs before the wall, and a wait for host-delivered values is
# something) and below the worktree populate (that checkout runs the repo's
# own post-checkout hook and smudge filters, which have no business inheriting
# a credential), but ABOVE the firstrun loop, so a hook -- a child process,
# which inherits these exports -- sees a value the user delivered with
# `byre credentials`: a login hook that stands down on an env credential can
# only do that if the credential is already in its env. Then again after the
# env.d loop, so credential exports still win env collisions (ADR 0028
# ordering). Values export byte-exact, no shell re-evaluation.
# The first call's placement also keeps the host's "byre: credentials:
# delivered." line (internal/commands/credentials.go credDeliveredLine,
# printed once the inject exec returns) off a hook's open prompt: the launcher
# blocks on .done until the receiver has written it, so no hook has started
# when it lands. The residual is milliseconds wide: the receiver writes .done
# and THEN exits while the host prints after its exec returns, so the
# launcher's next 0.2s poll can win that race and a hook that prompts
# instantly may still share its first line with the host's.
#
# NAMING. Every variable the launcher assigns from here to the exec is
# BYRE_-named, and the key check below refuses BYRE_*, so no delivered key can
# alias one. The key grammar admits every OTHER shell name, and an export
# attribute survives a later plain assignment: a loop counter spelled
# cred_exported would be read back as the launcher's own state (a secret
# evaluated as arithmetic kills the launcher under set -u and echoes part of it
# in bash's error), a key slot spelled cred_key would reach the agent
# rewritten, and a firstrun loop variable spelled `hook` would hand every hook
# the launcher's path in place of the user's `hook` credential. Function names
# need no prefix: `export` assigns variables, and a function is a separate
# namespace no export can clobber.
#
# The seams resolve ONCE, here, so both calls read the same delivery. The env
# overrides are test seams (gate precedent); a user setting them re-points
# byre's own delivery, which is theirs to do. Readonly because the key check
# guards only the manifest: env.d is SOURCED into this shell, under set +eu,
# between the two calls, so a hook that reassigned BYRE_cred_expect to ""
# would turn the second pass into a no-op (its own export then beats the
# credential, against ADR 0028), one that re-pointed BYRE_cred_dir would fail
# a good delivery closed after the hooks ran, and an unset would kill the
# launcher on nounset. Readonly makes each of those a refused assignment the
# hook's own set +eu shrugs off: non-POSIX bash treats the assignment as a
# non-fatal error -- it abandons the rest of that one command in the hook,
# and the hook and this launcher carry on (POSIX mode would exit; the
# launcher runs under `bash`, never `sh`/posix).
BYRE_cred_expect="${BYRE_CRED_EXPECT:-}"
BYRE_cred_dir="${BYRE_CRED_DIR:-/run/byre}"
BYRE_cred_wait="${BYRE_CRED_WAIT:-20}"
readonly BYRE_cred_expect BYRE_cred_dir BYRE_cred_wait
byre_credentials_apply() {
  [ -n "$BYRE_cred_expect" ] || return 0
  SECONDS=0
  while [ ! -e "$BYRE_cred_dir/.done" ] && [ "$SECONDS" -lt "$BYRE_cred_wait" ]; do
    sleep 0.2
  done
  if [ ! -e "$BYRE_cred_dir/.done" ] || [ ! -r "$BYRE_cred_dir/manifest" ]; then
    echo "byre: credentials were expected but did not arrive within ${BYRE_cred_wait}s — refusing to launch without them (failing closed)." >&2
    echo "byre: (a restarted box never gets them: the session tmpfs empties, and the passphrase is only asked for at \`byre develop\`. Re-run it. To launch deliberately without: \`byre develop --credentials=skip\`.)" >&2
    exit 1
  fi
  # The manifest is byre-authored, one "KEY kind" line per delivered value:
  # the identifier a value travels under IS its config key, so there is no
  # second name to map. A line this loop cannot honor is a CORRUPT DELIVERY,
  # never a row to drop: exporting the rest would run the agent with a subset
  # of the credentials the config declared -- the same silently-underequipped
  # box the wait above refuses -- so every rejection exits, same direction as
  # the launch gate.
  #
  # The message names the manifest LINE and nothing from it. A manifest that
  # is not byre's is a manifest whose bytes may BE a credential, and echoing
  # the offending key or kind is how the value this gate protects reaches the
  # terminal.
  cred_fail() {
    echo "byre: credentials: the delivered manifest is not one byre wrote (line $1: $2) — refusing to launch on a partial credential set (failing closed)." >&2
    echo "byre: (re-run \`byre develop\` to deliver them again. To launch deliberately without: \`byre develop --credentials=skip\`.)" >&2
    exit 1
  }
  # An export failure is not the manifest's fault -- byre wrote the key, and
  # bash refused the name or did not keep the value -- so it says that
  # instead, naming the line and neither the key nor the value. The likeliest
  # cause by far is an env.d hook that made the name readonly or gave it an
  # attribute, which the second pass then meets.
  cred_export_fail() {
    echo "byre: credentials: the value on line $1 could not be exported under the name it was delivered with (a launch env hook may have redefined that name) — refusing to launch on a partial credential set (failing closed)." >&2
    echo "byre: (re-run \`byre develop\` to deliver them again. To launch deliberately without: \`byre develop --credentials=skip\`.)" >&2
    exit 1
  }
  # The backstop behind the bash-owned refusal below: whatever that list
  # misses must still fail CLOSED, without the value in any message. Takes
  # line, key, value as positional parameters, so it holds no variable a
  # delivered key could name. The subshell probe goes first because some
  # failures are not a false return bash lets `if` catch: an arithmetic
  # assignment error (RANDOM, OPTIND) exits the shell even under set +e, and
  # a readonly export exits it under set -e, either way silently with stderr
  # discarded and never through a refusal. A probe that dies costs only the
  # probe. The read-back then insists on a plain exported scalar holding the
  # exact bytes: an export can return 0 and still leave a dynamic value
  # (LINENO, SECONDS) or an array no child ever receives (BASH_REMATCH).
  byre_cred_export() {
    if ! (export -- "$2=$3") 2>/dev/null || ! export -- "$2=$3" 2>/dev/null; then
      cred_export_fail "$1"
    fi
    # The attribute check is `declare -p`, not bash 4.4's ${!2@a}: the macOS
    # CI leg runs this under /bin/bash 3.2. An exported plain scalar prints
    # exactly `declare -x NAME=...`; an array prints -ax, an integer -ix, a
    # readonly -rx, an unexported one `declare --`. The output (which holds
    # the value) never leaves the substitution.
    if [ -z "${!2+set}" ] || [ "${!2}" != "$3" ]; then
      cred_export_fail "$1"
    fi
    case "$(declare -p -- "$2" 2>/dev/null)" in
    "declare -x $2="*) ;;
    *) cred_export_fail "$1" ;;
    esac
  }
  # Byre's manifest ends every line, the last one included. A final byte that
  # is not a newline means the delivery was CUT SHORT -- and `read` would end
  # the loop on that partial line without failing, exporting only the rows
  # before it: the partial credential set every rejection below exits over,
  # reached by silence instead of by a bad line. Refused whole, here, so the
  # loop only ever sees terminated records.
  #
  # Read with a BUILTIN, never `tail`: PATH is a name a credential may carry
  # (byre discloses that rather than reserving it, P1), and the SECOND pass
  # runs with every delivered value already exported -- so an external command
  # here is one a delivered value can make unfindable. `tail` was exactly
  # that: with PATH re-pointed its substitution came back empty, this check
  # passed vacuously, and a manifest shortened between the two passes exported
  # a partial set. The only external command left in this function is the wait
  # loop's `sleep`, which the second pass never reaches (.done exists by then)
  # and the first pass runs before any credential is exported. PAST this
  # function, `bash "$hook"` and the final exec do resolve on PATH: a
  # credential that re-points those breaks the user's own box, which is theirs
  # to do (P1), and byre says so rather than reserving the name.
  BYRE_cred_nl='
'
  BYRE_cred_manifest=""
  # A read to the NUL delimiter returns 0 only when it FOUND one, which byre's
  # composer never writes -- and everything past it stays unread, so the
  # newline check below would judge a prefix while the loop went on to drop the
  # unterminated record after it: the partial set, reached by silence. Refused
  # as its own rule. A non-zero return is the ordinary end of file, with the
  # whole manifest in the variable.
  if IFS= read -rd '' BYRE_cred_manifest <"$BYRE_cred_dir/manifest"; then
    cred_fail 0 "it holds a NUL, a byte byre never writes"
  fi
  case "$BYRE_cred_manifest" in
  # Empty: the "named no credentials at all" refusal below is the honest one.
  "") ;;
  *"$BYRE_cred_nl") ;;
  *) cred_fail 0 "it does not end in a newline, so the delivery was truncated" ;;
  esac
  BYRE_cred_manifest=""
  BYRE_cred_lineno=0
  BYRE_cred_exported=0
  # The split is pinned to a space (byre's composer writes "KEY kind"), not
  # inherited: the key grammar admits IFS, PATH, HOME and every other shell
  # name, and since the first pass exports above firstrun, a credential so
  # named now reaches every hook (and env.d, sourced into this shell) too --
  # byre discloses that, it does not reserve the names (P1: a user's own key
  # is theirs to name). What it must not do is let one re-parse its own
  # manifest: an inherited IFS of, say, ":" makes this read take "KEY env"
  # as one key, so a delivery that exported clean on the first pass failed
  # closed on the second, after the hooks had run.
  while IFS=' ' read -r BYRE_cred_key BYRE_cred_kind; do
    BYRE_cred_lineno=$((BYRE_cred_lineno + 1))
    # An empty line is a line byre's composer never writes, which by this
    # block's own rule makes the delivery corrupt -- and skipping it would
    # skip it SILENTLY, the one direction every other arm here refuses.
    [ -n "$BYRE_cred_key" ] || cred_fail "$BYRE_cred_lineno" "the line is empty"
    if ! [[ "$BYRE_cred_key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || [[ "$BYRE_cred_key" == BYRE_* ]]; then
      cred_fail "$BYRE_cred_lineno" "the export key is not a variable name byre would have written"
    fi
    # Names bash itself owns cannot carry a value to the agent faithfully
    # (why, per name: config.bashOwnedCredentialNames, which `byre credentials
    # set` refuses the same list from). Refused like BYRE_*, naming the line
    # only. The arm below is pinned byte-identical to
    # config.BashOwnedCredentialPattern by test; edit the Go list.
    case "$BYRE_cred_key" in
    BASH_* | COMP_* | READLINE_* | BASH | BASHPID | BASHOPTS | SECONDS | RANDOM | SRANDOM | LINENO | EPOCHSECONDS | EPOCHREALTIME | HISTCMD | OPTIND | OPTERR | UID | EUID | PPID | GROUPS | DIRSTACK | FUNCNAME | PIPESTATUS | SHELLOPTS | SHLVL | PWD | OLDPWD | PS0 | PS1 | PS2 | PS3 | PS4 | PROMPT_COMMAND)
      cred_fail "$BYRE_cred_lineno" "the export key is a name bash itself owns"
      ;;
    esac
    BYRE_cred_file="$BYRE_cred_dir/credentials/$BYRE_cred_key"
    # A value file that is missing or is not a regular file means the
    # delivery for this row did not land; there is nothing to export and no
    # honest way to continue.
    [ -f "$BYRE_cred_file" ] || cred_fail "$BYRE_cred_lineno" "its value never landed on the session tmpfs"
    # And one that cannot be OPENED fails closed as well, for both kinds: the
    # read below cannot report it (`read -rd ''` returns non-zero at EOF on
    # every successful read of a NUL-free value, which is what the `|| true`
    # is for, so it swallows an open failure too and exports the empty
    # string), and a file kind would hand the agent a path it cannot read.
    { : <"$BYRE_cred_file"; } 2>/dev/null ||
      cred_fail "$BYRE_cred_lineno" "its value could not be read from the session tmpfs"
    case "$BYRE_cred_kind" in
    env)
      # Byte-exact: read to EOF (env values are NUL-free by rule);
      # $(cat) would strip trailing newlines the value may carry.
      BYRE_cred_val=""
      IFS= read -rd '' BYRE_cred_val <"$BYRE_cred_file" || true
      byre_cred_export "$BYRE_cred_lineno" "$BYRE_cred_key" "$BYRE_cred_val"
      BYRE_cred_val=""
      ;;
    file)
      byre_cred_export "$BYRE_cred_lineno" "$BYRE_cred_key" "$BYRE_cred_file"
      ;;
    *)
      cred_fail "$BYRE_cred_lineno" "the delivery kind is neither env nor file"
      ;;
    esac
    BYRE_cred_exported=$((BYRE_cred_exported + 1))
  done <"$BYRE_cred_dir/manifest"
  # BYRE_CRED_EXPECT is only set when byre scheduled a non-empty set, so a
  # manifest that named nothing is the same corrupt delivery as a bad line.
  if [ "$BYRE_cred_exported" -eq 0 ]; then
    cred_fail 0 "it named no credentials at all"
  fi
}
byre_credentials_apply

# First-run hooks — agent skills drop scripts here. They run as the dev user
# (the launcher is unprivileged), so a hook does its own user-level setup directly
# (codex device-auth login → the .codex volume; devlog → /workspace). A hook that
# needs root is not supported: skills declaring privileged setup would need an
# explicit, status-visible grant, not a blanket-root entrypoint.
# The dir override is a test seam (the gate-file/env.d precedent) — without it, the
# launcher tests execute the REAL hooks of whatever box runs the suite, and a
# hook that legitimately prompts (a login on a box whose credential died)
# hangs them.
BYRE_firstrun_dir="${BYRE_FIRSTRUN_DIR:-/etc/byre/firstrun.d}"
if [ -d "$BYRE_firstrun_dir" ]; then
  for BYRE_hook in "$BYRE_firstrun_dir"/*; do
    # Unreadable entries -- and the literal "$BYRE_firstrun_dir/*" an unmatched glob
    # leaves behind -- are a silent no-op. A hook that RAN and failed is not:
    # the launcher continues (one skill's broken setup must not cost the user
    # their box) but says so, because a hook failing invisibly is how a box
    # boots subtly wrong. The `if bash ...; then :; else` shape is load-bearing
    # under `set -e`: the naive `bash "$BYRE_hook"; BYRE_hook_status=$?` kills
    # the launcher on the failing hook, the exact inversion of best-effort.
    if [ -r "$BYRE_hook" ]; then
      if bash "$BYRE_hook"; then
        :
      else
        BYRE_hook_status=$?
        printf 'byre: firstrun hook %q exited %d (continuing)\n' "$BYRE_hook" "$BYRE_hook_status" >&2
      fi
    fi
  done
fi

# Launch env hooks — skills drop scripts here to put env into the AGENT
# process (a firstrun hook runs in its own process, so it can't). Sourced
# (not executed) in glob order, after firstrun hooks and before the
# credential re-apply and exec, still as the unprivileged dev user. Hooks owe
# this shell the ADR 0028 purity contract: the environment they leave behind
# is their only lasting effect. errexit/nounset are suspended around each
# source so strict mode does not turn a pure hook's benign unset reference
# into a dead launcher -- that suspension is a courtesy to hooks that KEEP
# the contract, not a container for ones that break it, and best-effort is
# guaranteed only to the former. First user: claude-shared-auth exports
# CLAUDE_CODE_OAUTH_TOKEN from its identity volume (ADR 0017). The dir
# override is a test seam, per the gate precedent.
BYRE_envd_dir="${BYRE_ENVD_DIR:-/etc/byre/env.d}"
if [ -d "$BYRE_envd_dir" ]; then
  for BYRE_envhook in "$BYRE_envd_dir"/*.sh; do
    if [ -r "$BYRE_envhook" ]; then
      set +eu
      # shellcheck disable=SC1090
      . "$BYRE_envhook"
      set -eu
    fi
  done
fi

# Credential re-apply — the second call (the block above firstrun holds the
# function and the reasoning). The sentinel already exists, so this does not
# wait; it re-validates the same tree under the same seam values and re-exports
# byte-exact, so a credential target beats an env.d hook exporting the same
# variable -- the ADR 0028 "credential exports win env collisions" ordering,
# unchanged. byre itself writes nothing to the session tmpfs between the two
# calls (the receiver is one-shot and wrote .done LAST). First-run code runs as
# the box user and could -- it already holds these exports, so it gains nothing
# by it.
byre_credentials_apply

# Agent command: explicit run args > recorded agent command > login shell.
# /etc/byre/agent-cmd is an *executable script* an agent skill installs;
# executing it (rather than word-splitting its text) preserves quoting/spaces.
if [ "$#" -gt 0 ]; then
  BYRE_cmd=("$@")
elif [ -x /etc/byre/agent-cmd ]; then
  BYRE_cmd=(/etc/byre/agent-cmd)
else
  BYRE_cmd=(bash -l)
fi

exec "${BYRE_cmd[@]}"
