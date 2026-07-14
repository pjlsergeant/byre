#!/bin/bash
# byre's codex MCP adapter (ADR 0033): derive per-invocation `-c` overrides
# from the canonical /etc/byre/mcp.json and exec codex. Pure injection — no
# state writes, exact per-session convergence by construction, same contract
# as the claude skill's --mcp-config flag.
#
# Field mapping (live-verified on codex 0.144.3, 2026-07-15):
#   stdio:  mcp_servers.<name>.command / .args
#   remote: mcp_servers.<name>.url
#   env:    mcp_servers.<name>.env_vars  <- the file's x_byre_env NAMES.
#           codex passes MCP servers a SCRUBBED env (probe: 4 vars), unlike
#           claude's full inheritance, so declared names must be forwarded
#           by-name here; values still come from the box env
#           (env_from_host / [env]), never from any byre file.
#
# Encoding: `-c` values parse as TOML. JSON string/array-of-string encoding
# is valid TOML for the same, and byre validation bans control characters in
# names/commands/urls, so jq's tojson output is line-safe and TOML-safe.
set -eu

# Overridable for tests (and hand-wiring experiments); byre boxes use the bake.
MCP=${BYRE_MCP_CONFIG:-/etc/byre/mcp.json}

flags=()
if [ -r "$MCP" ]; then
  while IFS= read -r line; do
    flags+=(-c "$line")
  done < <(jq -r '
    .mcpServers | to_entries | sort_by(.key)[] |
    ( if .value.url then
        "mcp_servers.\(.key).url=\(.value.url | tojson)"
      else
        "mcp_servers.\(.key).command=\(.value.command | tojson)",
        "mcp_servers.\(.key).args=\(.value.args // [] | tojson)"
      end ),
    ( if ((.value.x_byre_env // []) | length) > 0 then
        "mcp_servers.\(.key).env_vars=\(.value.x_byre_env | tojson)"
      else empty end )
  ' "$MCP")
fi

exec codex "${flags[@]}" "$@"
