#!/bin/bash
# byre's opencode launch adapter: build an OPENCODE_CONFIG_CONTENT carrying
# the canonical /etc/byre/mcp.json servers (ADR 0033) and the baked agent
# context as an `instructions` entry (ADR 0046), then exec opencode. opencode
# deep-MERGES OPENCODE_CONFIG_CONTENT over global + project config and
# CONCATENATES instructions arrays (source-verified, config.ts
# mergeConfigConcatArrays), so injected content COMPOSES with the user's own
# config and never replaces it. Pure injection — no state writes, exact
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

# Agent context (ADR 0046): the baked file rides opencode's `instructions`
# config key — entries APPEND to the system context ("Instructions from:
# <path>" blocks) and instruction arrays CONCAT across layers, so the user's
# own instructions survive. A missing file is a silent no-op in opencode
# (fs.glob include:"file"), but the bake makes the file unconditional.
# Per-session additions ($BYRE_SESSION_CONTEXT) don't ride this file-path
# channel — not delivered for opencode, recorded in the ADR. When the box (or
# a user) has ALREADY set OPENCODE_CONFIG_CONTENT, byre's content deep-merges
# ON TOP: mcp servers win per-name (the precedence opencode itself gives this
# env layer), instructions UNION.
CTX=${BYRE_AGENT_CONTEXT:-/etc/byre/agent-context.md}
base=${OPENCODE_CONFIG_CONTENT:-'{}'}
printf '%s' "$base" | jq empty 2>/dev/null || base='{}'
OPENCODE_CONFIG_CONTENT=$(printf '%s' "$base" \
  | jq -c --argjson mcp "$byre_mcp" --arg ctx "$CTX" '
      . * {mcp: ((.mcp // {}) * $mcp)}
      | .instructions = ((.instructions // []) + (if ((.instructions // []) | index($ctx)) then [] else [$ctx] end))
      | if .mcp == {} then del(.mcp) else . end')
export OPENCODE_CONFIG_CONTENT

exec opencode "$@"
