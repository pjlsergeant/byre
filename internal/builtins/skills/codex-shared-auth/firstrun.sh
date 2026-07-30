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

bash "$RECONCILE" startup || true
exit 0
