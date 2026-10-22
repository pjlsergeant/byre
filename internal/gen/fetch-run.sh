#!/bin/sh
# byre-fetch-run <url> <interpreter>: download an installer script, check it
# is a script, then run it with <interpreter> (sh, bash). Build-only: agent
# skills install their vendor CLIs through it in a RUN line.
#
# Installers are fetched then run, never piped: a pipeline exits with the
# shell's status, so a failed fetch would run an empty script, exit 0, and be
# cached by Docker as a good layer with no binary in it.
#
# The fetched file must start with a `#!` shebang because the download host
# can answer 200 with an error page or an empty body, and `sh file` on prose
# or nothing can also exit 0.
#
# --retry-max-time bounds the retries because --retry otherwise sleeps for
# the server's whole Retry-After, with no cap, before every attempt.
#
# BYRE_FETCH_RUN_RETRIES overrides the retry count (default 5).
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: byre-fetch-run <url> <interpreter>" >&2
	exit 2
fi
url=$1
interpreter=$2

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

if ! curl -fsSL --retry "${BYRE_FETCH_RUN_RETRIES:-5}" --retry-all-errors \
	--retry-max-time 120 -o "$tmp" "$url"; then
	echo "byre-fetch-run: fetch failed: $url" >&2
	exit 1
fi

if [ "$(head -c 2 "$tmp")" != '#!' ]; then
	first=$(head -n 1 "$tmp" | cut -c 1-80)
	echo "byre-fetch-run: $url did not return a script (first line: ${first:-<empty>})" >&2
	exit 1
fi

status=0
"$interpreter" "$tmp" || status=$?
exit "$status"
