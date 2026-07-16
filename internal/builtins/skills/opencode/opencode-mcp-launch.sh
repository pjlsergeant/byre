#!/bin/bash
# byre's opencode MCP adapter (ADR 0033): build an OPENCODE_CONFIG_CONTENT from
# the canonical /etc/byre/mcp.json and exec opencode. opencode deep-MERGES
# OPENCODE_CONFIG_CONTENT over global + project config (source-verified,
# config.ts load order, 1.18.3), so injected servers COMPOSE with the user's
# own config and never replace it. Pure injection — no state writes, exact
# per-session convergence by construction, same contract as the codex adapter.
#
# Schema (opencode core v1 config/mcp.ts): the top-level `mcp` map, keyed by
# name, discriminated on `type`:
#   stdio  -> mcp.<name> = {type:"local",  command:[cmd, arg...]}
#   remote -> mcp.<name> = {type:"remote", url, headers:{Name:Value}}
# Note opencode's `command` is ONE combined array (binary + args), unlike
# codex's split command/args.
#
# Env mapping: opencode spawns local MCP servers with `{...process.env,
# ...mcp.environment}` (mcp/index.ts) — i.e. they INHERIT the full box env,
# unlike codex's scrubbed env. So the file's `x_byre_env` NAMES are already
# visible to a local server and need NO `environment` block emitted here.
# Remote `headers` take literal VALUES only (opencode has no by-name/bearer
# tier), so `${VAR}` refs are expanded HERE at launch ($ENV); an unset ref
# stays literal (claude/codex expansion parity). Expanded header values ride
# OPENCODE_CONFIG_CONTENT — equivalent exposure to the box env the agent
# already reads; the baked mcp.json stays free of byre-placed secrets.
set -eu

# Overridable for tests (and hand-wiring experiments); byre boxes use the bake.
MCP=${BYRE_MCP_CONFIG:-/etc/byre/mcp.json}

byre_mcp="{}"
if [ -r "$MCP" ]; then
  byre_mcp=$(jq -c '
    def expand: gsub("\\$\\{(?<n>[A-Za-z_][A-Za-z0-9_]*)\\}"; ($ENV[.n] // "${\(.n)}"));
    (.mcpServers // {}) | to_entries | map(
      .key as $k |
      if .value.url then
        { ($k): ({ type: "remote", url: .value.url }
          + (if (.value.headers // {}) != {}
             then { headers: (.value.headers | with_entries(.value |= expand)) }
             else {} end)) }
      else
        { ($k): { type: "local", command: ([.value.command] + (.value.args // [])) } }
      end
    ) | add // {}
  ' "$MCP")
fi

# Only inject when there is at least one server. An empty `mcp` map would merge
# harmlessly, but skipping keeps a no-MCP box byte-identical to plain opencode
# (the codex "empty set = zero flags" contract). When the box (or a user) has
# ALREADY set OPENCODE_CONFIG_CONTENT, deep-merge byre's servers ON TOP so the
# injection is additive rather than clobbering — byre keys win per-name, the
# same precedence opencode itself gives this env layer.
if [ "$(printf '%s' "$byre_mcp" | jq -r 'length')" != "0" ]; then
  base=${OPENCODE_CONFIG_CONTENT:-'{}'}
  printf '%s' "$base" | jq empty 2>/dev/null || base='{}'
  OPENCODE_CONFIG_CONTENT=$(printf '%s' "$base" \
    | jq -c --argjson mcp "$byre_mcp" '. * {mcp: ((.mcp // {}) * $mcp)}')
  export OPENCODE_CONFIG_CONTENT
fi

exec opencode "$@"
