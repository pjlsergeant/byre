# byre: give login shells (e.g. `byre shell`) the same env.d-provided
# environment the launcher gives the agent. Sourced by /etc/profile for every
# login shell. env.d hooks are PURE env-setters (any command/prompt/side-effect
# lives in firstrun.d, run by the launcher only), so sourcing them in any login
# shell is safe and quiet -- no strict-mode guarding needed here. The
# BYRE_ENVD_DIR override mirrors the launcher's test seam.

# First, restore the image's ENV PATH: Debian's /etc/profile unconditionally
# resets PATH for login shells, dropping base-image entries (golang's
# /usr/local/go/bin -- a go-template box had no `go` in `byre shell`). The
# agent never loses them (the launcher execs with the container env, no
# /etc/profile), so without this a human's shell sees a poorer PATH than the
# agent's. /etc/byre/image-path is baked at image build time (gen's tail).
# The merge is ADDITIVE -- missing entries only, image order preserved,
# prepended so image toolchains win name clashes the same way they do for the
# agent. BYRE_IMAGE_PATH_FILE is a test seam like BYRE_ENVD_DIR.
_byre_rest=$(cat "${BYRE_IMAGE_PATH_FILE:-/etc/byre/image-path}" 2>/dev/null)
_byre_add=""
while [ -n "$_byre_rest" ]; do
  case "$_byre_rest" in
    *:*) _byre_dir="${_byre_rest%%:*}"; _byre_rest="${_byre_rest#*:}" ;;
    *)   _byre_dir="$_byre_rest"; _byre_rest="" ;;
  esac
  [ -n "$_byre_dir" ] || continue
  case ":$PATH:$_byre_add" in
    *":$_byre_dir:"*) ;;
    *) _byre_add="$_byre_add$_byre_dir:" ;;
  esac
done
if [ -n "$_byre_add" ]; then
  PATH="$_byre_add$PATH"
  export PATH
fi
unset _byre_rest _byre_add _byre_dir

for _byre_envhook in "${BYRE_ENVD_DIR:-/etc/byre/env.d}"/*.sh; do
  [ -r "$_byre_envhook" ] && . "$_byre_envhook"
done
unset _byre_envhook
