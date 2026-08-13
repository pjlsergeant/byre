#!/usr/bin/env bash
# byre credential receiver — the in-box end of the credential delivery
# stream.
#
# byre pipes the deliverable set over `engine exec -i` stdin as a framed
# text stream; this script writes each value under the session tmpfs and a
# .done sentinel LAST, so the launcher's bounded wait never observes a
# half-written tree. Payloads ride base64 on a single line, which keeps the
# framing line-oriented (binary-safe without byte-count parsing in bash).
#
# Stream grammar:
#   byre-credentials 1
#   item manifest          the export manifest, exactly once, first
#   <base64, one line>
#   item <name>            name = a credential's CONFIG KEY
#   <base64, one line>
#   ...
#   done
#
# "manifest" is POSITIONAL, not an item name the loop below accepts. A
# credential's config key is an environment variable name, and "manifest" is a
# legal one — so an item loop that honoured the name would let a credential's
# VALUE land on the manifest the launcher parses, delivering nothing and
# handing the launcher a secret to read as export lines. The frame that may
# write outside the credentials dir is the first one and only the first one.
#
# A stream that ends without "done" leaves no sentinel, and the launcher then
# fails the launch closed. This is plain transport correctness — the agent is
# HANDED the delivered values (the contract), so nothing here verifies or
# polices the box; the name check below only keeps a corrupt frame from
# writing outside the credentials dir.
set -euo pipefail

DIR="${BYRE_CRED_DIR:-/run/byre}"
umask 077

IFS= read -r header || exit 1
if [ "$header" != "byre-credentials 1" ]; then
  echo "byre-credential-receiver: not a credential stream I understand (${header%% *}...)" >&2
  exit 1
fi
mkdir -p "$DIR/credentials"

# The prologue: the manifest frame, required and consumed here.
IFS= read -r line || exit 1
if [ "$line" != "item manifest" ]; then
  echo "byre-credential-receiver: the stream must open with its manifest frame" >&2
  exit 1
fi
IFS= read -r b64 || exit 1
printf '%s' "$b64" | base64 -d >"$DIR/manifest"

while IFS= read -r line; do
  case "$line" in
  done)
    : >"$DIR/.done"
    exit 0
    ;;
  "item "*)
    name="${line#item }"
    # Refused BEFORE the payload line is read, so a refused frame writes
    # nothing and the manifest already on disk stays byre's.
    if [ "$name" = manifest ]; then
      echo "byre-credential-receiver: refusing a second manifest frame" >&2
      exit 1
    fi
    if ! [[ "$name" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      echo "byre-credential-receiver: refusing malformed item name" >&2
      exit 1
    fi
    IFS= read -r b64 || exit 1
    printf '%s' "$b64" | base64 -d >"$DIR/credentials/$name"
    ;;
  *)
    echo "byre-credential-receiver: malformed frame" >&2
    exit 1
    ;;
  esac
done

# EOF without "done": incomplete delivery, no sentinel written.
exit 1
