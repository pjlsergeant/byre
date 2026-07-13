# byre: give login shells (e.g. `byre shell`) the same env.d-provided
# environment the launcher gives the agent. Sourced by /etc/profile for every
# login shell. env.d hooks are PURE env-setters (any command/prompt/side-effect
# lives in firstrun.d, run by the launcher only), so sourcing them in any login
# shell is safe and quiet -- no strict-mode guarding needed here. The
# BYRE_ENVD_DIR override mirrors the launcher's test seam.
for _byre_envhook in "${BYRE_ENVD_DIR:-/etc/byre/env.d}"/*.sh; do
  [ -r "$_byre_envhook" ] && . "$_byre_envhook"
done
unset _byre_envhook
