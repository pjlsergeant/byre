#!/usr/bin/env bash
# Re-pack a skill that lives in this repo, in place.
#
#   scripts/repack-skill.sh [skill-dir]     # default: skills/inttest
#
# Each skill.toml here is committed PACKED: the [[package.files]] list is
# generated with payload hashes, and `Pack` writes an EXHAUSTIVE list (every
# file in the directory). So adding, removing or editing any payload file
# leaves the committed manifest stale until this runs. The unit suite pins
# this: TestRepoSkillsAreCommittedPacked (internal/packages) fails on a stale
# manifest and names this script as the remedy; `byre skill install` is the
# late backstop.
#
# Uses a byre on PATH if there is one, else builds from this working tree --
# which is what makes this usable inside a byre box, where there is no byre
# binary but there is Go and the source.
#
# Writes to a temp file and moves it into place only on success. The obvious
# one-liner (`... > skills/<x>/skill.toml`) truncates the manifest BEFORE the
# pack reads it, so the copy it packs is empty and the original is destroyed.
set -euo pipefail

cd "$(dirname "$0")/.."
SKILL_DIR="${1:-skills/inttest}"
SKILL_DIR="${SKILL_DIR%/}"

[ -f "$SKILL_DIR/skill.toml" ] || {
  echo "repack-skill: no skill.toml in $SKILL_DIR" >&2
  exit 1
}

# The qualified id the manifest declares -- pack refuses if it disagrees with
# the catalog id, so the temp home must be laid out to match it.
id=$(awk -F'"' '/^id[[:space:]]*=/ {print $2; exit}' "$SKILL_DIR/skill.toml")
[ -n "$id" ] || { echo "repack-skill: $SKILL_DIR/skill.toml declares no id" >&2; exit 1; }
owner="${id%%/*}"
[ "$owner" != "$id" ] || { echo "repack-skill: id '$id' is not owner-qualified" >&2; exit 1; }

if command -v byre >/dev/null 2>&1; then
  byre() { command byre "$@"; }
else
  byre() { go run ./cmd/byre "$@"; }
fi

# pack operates on a LOCAL package; a copy under the real ~/.byre/skills would
# contest the installed id, hence the throwaway BYRE_HOME.
home=$(mktemp -d)
out=$(mktemp)
trap 'rm -rf "$home" "$out"' EXIT

mkdir -p "$home/skills/$owner"
cp -R "$SKILL_DIR" "$home/skills/$owner/"

BYRE_HOME="$home" byre skill pack "$id" > "$out"
[ -s "$out" ] || { echo "repack-skill: pack produced nothing; $SKILL_DIR/skill.toml left alone" >&2; exit 1; }

mv "$out" "$SKILL_DIR/skill.toml"
trap 'rm -rf "$home"' EXIT
echo "repack-skill: re-packed $id -> $SKILL_DIR/skill.toml"
