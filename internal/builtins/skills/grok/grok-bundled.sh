#!/bin/sh
# grok bundled-skills bridge firstrun hook — idempotently asserts, every
# launch, that $GROK_HOME/bundled points at the image-side ~/.grok/bundled.
#
# WHY: with GROK_HOME split from ~/.grok (the binary/state decoupling), grok
# 0.2.93 still EXTRACTS its bundled product packs (the review / execute-plan /
# design / pr-babysit skills, personas, roles) into $HOME/.grok/bundled — the
# image tree — while skill DISCOVERY reads $GROK_HOME/bundled. Without this
# link the bundled skills silently never appear (reproduced in-box
# 2026-07-09: `grok inspect` lists them only with the link present). The
# extraction is the ONE write grok makes back into ~/.grok under the split;
# it lands in the container's writable layer, so it costs a re-extract per
# rebuild, never a masked binary.
#
# If a future grok version extracts into $GROK_HOME/bundled directly, that
# shows up as a REAL directory here — hands off, the quirk is fixed upstream.
# (No command -v guard: this hook only ships when the grok skill is enabled,
# and a dangling link in a grok-less box is inert — the shared-auth precedent.)
export GROK_HOME="${GROK_HOME:-/home/dev/.grok-home}"
b="$GROK_HOME/bundled"
mkdir -p "$GROK_HOME" 2>/dev/null || exit 0
# A real directory (not a symlink) means grok manages it in place — leave it.
if [ -d "$b" ] && [ ! -L "$b" ]; then exit 0; fi
if [ ! -L "$b" ] || [ "$(readlink "$b")" != "/home/dev/.grok/bundled" ]; then
  rm -f "$b" 2>/dev/null
  ln -s /home/dev/.grok/bundled "$b" 2>/dev/null || true
fi
exit 0
