#!/usr/bin/env bash
# byre credential receiver — the in-box end of the credential delivery
# stream (wip/secure-credentials.md "Launch and delivery" step 3).
#
# byre pipes the deliverable set over `engine exec -i` stdin as a framed
# text stream; this script writes each value under the session tmpfs and a
# .done sentinel LAST, so the launcher's bounded wait never observes a
# half-written tree. Payloads ride base64 on a single line, which keeps the
# framing line-oriented (binary-safe without byte-count parsing in bash).
#
# Stream grammar:
#   byre-credentials 1
#   item <name>            name = "manifest" or a credential name
#   <base64, one line>
#   ...
#   done
#
# A stream that ends without "done" leaves no sentinel: the launcher
# fail-opens and the box runs without credentials. This is plain transport
# correctness — the agent is HANDED the delivered values (the contract), so
# nothing here verifies or polices the box; the name check below only keeps
# a corrupt frame from writing outside the credentials dir.
set -euo pipefail

DIR="${BYRE_CRED_DIR:-/run/byre}"
umask 077

IFS= read -r header || exit 1
if [ "$header" != "byre-credentials 1" ]; then
  echo "byre-credential-receiver: not a credential stream I understand (${header%% *}...)" >&2
  exit 1
fi
mkdir -p "$DIR/credentials"

while IFS= read -r line; do
  case "$line" in
  done)
    : >"$DIR/.done"
    exit 0
    ;;
  "item "*)
    name="${line#item }"
    IFS= read -r b64 || exit 1
    if [ "$name" = manifest ]; then
      dest="$DIR/manifest"
    elif [[ "$name" =~ ^[a-z][a-z0-9-]{0,62}$ ]]; then
      dest="$DIR/credentials/$name"
    else
      echo "byre-credential-receiver: refusing malformed item name" >&2
      exit 1
    fi
    printf '%s' "$b64" | base64 -d >"$dest"
    ;;
  *)
    echo "byre-credential-receiver: malformed frame" >&2
    exit 1
    ;;
  esac
done

# EOF without "done": incomplete delivery, no sentinel written.
exit 1
