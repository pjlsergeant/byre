#!/usr/bin/env bash
# byre-ccusage -- run ccusage over the agent state volumes of every byre
# project on this machine.
#
# Hostside tool. For each project registered in ~/.byre/projects/, copies the
# usage transcripts out of its per-agent state volumes (read-only mounts;
# credentials never leave the volume) into a temp dir, then points the
# matching ccusage subcommand at the copies.
#
# Agents covered: claude, codex, opencode, gemini. grok is not supported by
# ccusage (its local files lack reliable token usage), so there is nothing
# to run for it.

set -euo pipefail

usage() {
  cat <<'EOF'
usage: byre-ccusage [options] [ccusage args...]

  default            one machine-wide report per agent (all projects merged)
  --each             separate report per project per agent
  --agents a,b,...   subset of: claude codex opencode gemini (default: all)
  --engine NAME      container engine (default: docker, else podman)
  --keep             keep the extracted copies and print their location

Everything unrecognized is passed through to ccusage, e.g.:
  byre-ccusage monthly --since 20260701
  byre-ccusage --each --agents claude,codex daily
EOF
}

AGENTS="claude codex opencode gemini"
EACH=0
KEEP=0
ENGINE=""
PASS=()

while [ $# -gt 0 ]; do
  case "$1" in
    --each) EACH=1 ;;
    --keep) KEEP=1 ;;
    --engine) ENGINE="${2:?--engine needs a value}"; shift ;;
    --agents) AGENTS="${2:?--agents needs a value}"; AGENTS="${AGENTS//,/ }"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) PASS+=("$1") ;;
  esac
  shift
done

for a in $AGENTS; do
  case "$a" in
    claude|codex|opencode|gemini) ;;
    grok) echo "byre-ccusage: skipping grok -- ccusage has no grok support (its local files lack reliable token usage)" >&2
          AGENTS="$(printf '%s\n' $AGENTS | grep -v '^grok$' | tr '\n' ' ' || true)" ;;
    *) echo "byre-ccusage: unknown agent '$a' (know: claude codex opencode gemini)" >&2; exit 2 ;;
  esac
done

if [ -z "$ENGINE" ]; then
  if command -v docker >/dev/null 2>&1; then ENGINE=docker
  elif command -v podman >/dev/null 2>&1; then ENGINE=podman
  else echo "byre-ccusage: neither docker nor podman on PATH" >&2; exit 1
  fi
fi

if command -v ccusage >/dev/null 2>&1; then CCUSAGE=(ccusage)
elif command -v npx >/dev/null 2>&1; then CCUSAGE=(npx -y ccusage@latest)
else echo "byre-ccusage: ccusage not on PATH and no npx to fall back to" >&2; exit 1
fi

BYRE_HOME="${BYRE_HOME:-$HOME/.byre}"
PROJ_DIR="$BYRE_HOME/projects"
if [ ! -d "$PROJ_DIR" ]; then
  echo "byre-ccusage: no projects dir at $PROJ_DIR" >&2
  exit 1
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/byre-ccusage.XXXXXX")"
cleanup() { [ "$KEEP" = 1 ] || rm -rf "$TMP"; }
trap cleanup EXIT

# The one subtree of each agent's state dir that holds usage transcripts.
# Copying only this keeps auth.json / .credentials.json inside the volume.
subtree() {
  case "$1" in
    claude)   echo projects ;;
    codex)    echo sessions ;;
    opencode) echo storage ;;
    gemini)   echo tmp ;;
  esac
}

run_ccusage() { # <agent> <datadir>
  local agent="$1" dir="$2"
  case "$agent" in
    claude)   CLAUDE_CONFIG_DIR="$dir"    "${CCUSAGE[@]}" claude   ${PASS[@]+"${PASS[@]}"} ;;
    codex)    CODEX_HOME="$dir"           "${CCUSAGE[@]}" codex    ${PASS[@]+"${PASS[@]}"} ;;
    opencode) OPENCODE_DATA_DIR="$dir"    "${CCUSAGE[@]}" opencode ${PASS[@]+"${PASS[@]}"} ;;
    gemini)   GEMINI_DATA_DIR="$dir/tmp"  "${CCUSAGE[@]}" gemini   ${PASS[@]+"${PASS[@]}"} ;;
  esac
}

# The extract container's image; pull up front so pull noise/failure doesn't
# get mistaken for "volume has no data" inside the extraction pipeline.
if ! "$ENGINE" image inspect alpine >/dev/null 2>&1; then
  echo "byre-ccusage: pulling alpine (one-time, for the extract container)..." >&2
  "$ENGINE" pull alpine >/dev/null
fi

shopt -s nullglob
PDIRS=("$PROJ_DIR"/*/)
shopt -u nullglob
TOTAL=${#PDIRS[@]}
if [ "$TOTAL" -eq 0 ]; then
  echo "byre-ccusage: no projects registered in $PROJ_DIR" >&2
  exit 0
fi
echo "byre-ccusage: scanning $TOTAL project(s) in $PROJ_DIR (engine: $ENGINE)" >&2

# Extract every project's per-agent transcripts to $TMP/per/<id>/<agent>/.
# Index lines: <id>\t<agent>\t<host path label>
INDEX="$TMP/index.tsv"
: > "$INDEX"
i=0
for pdir in "${PDIRS[@]}"; do
  i=$((i + 1))
  id="$(basename "$pdir")"
  label="$id"
  [ -f "$pdir/path" ] && label="$(cat "$pdir/path")"
  printf '[%d/%d] %s\n' "$i" "$TOTAL" "$label" >&2
  for agent in $AGENTS; do
    vol="byre-$id-.$agent"
    # inspect first: a bare `docker run -v` would CREATE a missing volume
    if ! "$ENGINE" volume inspect "$vol" >/dev/null 2>&1; then
      printf '    %-9s no volume\n' "$agent" >&2
      continue
    fi
    printf '    %-9s extracting...' "$agent" >&2
    dest="$TMP/per/$id/$agent"
    mkdir -p "$dest"
    sub="$(subtree "$agent")"
    "$ENGINE" run --rm -v "$vol":/src:ro alpine \
        tar -C /src -cf - "$sub" 2>/dev/null | tar -xf - -C "$dest" || true
    if [ -z "$(find "$dest" -type f -print -quit)" ]; then
      rm -rf "$dest"
      printf ' no usage data\n' >&2
      continue
    fi
    printf ' %s files, %s\n' \
      "$(find "$dest" -type f | wc -l | tr -d ' ')" \
      "$(du -sh "$dest" | cut -f1)" >&2
    printf '%s\t%s\t%s\n' "$id" "$agent" "$label" >> "$INDEX"
  done
done

if [ ! -s "$INDEX" ]; then
  echo "byre-ccusage: no usage data found in any project's agent volumes" >&2
  exit 0
fi

if [ "$EACH" = 1 ]; then
  while IFS=$'\t' read -r id agent label; do
    echo
    echo "==== $label [$agent] ===="
    run_ccusage "$agent" "$TMP/per/$id/$agent" || true
  done < "$INDEX"
else
  # Merge all projects into one tree per agent. Session/message filenames are
  # UUID- or date-based, so a plain overlay is collision-free -- except gemini,
  # whose tmp/<hash>/ dirs hash the CONTAINER path (/workspace for every box),
  # so each project's hash dirs get a unique <id>-- prefix instead.
  for agent in $AGENTS; do
    merged="$TMP/all/$agent"
    contributors=""
    while IFS=$'\t' read -r id a label; do
      [ "$a" = "$agent" ] || continue
      contributors="$contributors  $label"$'\n'
      src="$TMP/per/$id/$agent"
      mkdir -p "$merged"
      if [ "$agent" = gemini ]; then
        mkdir -p "$merged/tmp"
        for h in "$src/tmp"/*/; do
          [ -d "$h" ] && cp -a "$h" "$merged/tmp/$id--$(basename "$h")"
        done
      else
        cp -a "$src/." "$merged/"
      fi
    done < "$INDEX"
    [ -n "$contributors" ] || continue
    echo
    echo "==== $agent (all byre projects on this machine) ===="
    echo "from:"
    printf '%s' "$contributors"
    run_ccusage "$agent" "$merged" || true
  done
fi

[ "$KEEP" = 1 ] && echo "extracted copies kept at: $TMP" >&2 || true
