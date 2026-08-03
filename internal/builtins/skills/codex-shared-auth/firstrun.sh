#!/bin/bash
# codex-shared-auth firstrun hook (ADR 0017) — reconcile any regular local
# credential produced by Codex's clear-before-login flow, then assert that
# $CODEX_HOME/auth.json is a symlink into the machine-wide identity volume.
# Runs before the codex skill's own login hook.
set -u

RECONCILE=${BYRE_CODEX_AUTH_RECONCILE:-/usr/local/lib/byre-codex-auth-reconcile}
if [ ! -r "$RECONCILE" ]; then
  echo "byre codex-shared-auth: reconciliation helper $RECONCILE is unavailable — shared auth not asserted this launch." >&2
  exit 0
fi

# The timeout is the hard bound on adversarial-filesystem hangs inside the
# shared identity volume (the reconciler's own lock wait is 3s; anything
# approaching 30s is a planted-FIFO-class stall, not work).
TO=""
command -v timeout >/dev/null 2>&1 && TO="timeout 30"
$TO bash "$RECONCILE" startup
rc=$?
if [ "$rc" -eq 124 ]; then
  echo "byre codex-shared-auth: reconciliation timed out; shared auth not asserted this launch." >&2
fi
exit 0
