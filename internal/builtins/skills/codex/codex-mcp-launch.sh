#!/bin/bash
# byre's codex launch adapter: derive per-invocation `-c` overrides from the
# canonical /etc/byre/mcp.json (ADR 0033) and the baked agent context (ADR
# 0046), then exec codex. Pure injection — no state writes, exact per-session
# convergence by construction, same contract as the claude skill's flags.
#
# Field mapping (live-verified on codex 0.144.3, 2026-07-15):
#   stdio:  mcp_servers.<name>.command / .args
#   remote: mcp_servers.<name>.url
#   env:    mcp_servers.<name>.env_vars  <- the file's x_byre_env NAMES.
#           codex passes MCP servers a SCRUBBED env (probe: 4 vars), unlike
#           claude's full inheritance, so declared names must be forwarded
#           by-name here; values still come from the box env
#           (env_from_host / [env]), never from any byre file.
#   headers (remote): three tiers, most-by-name first —
#           `Authorization: Bearer ${VAR}`      -> bearer_token_env_var=VAR
#           a pure `${VAR}` value                -> env_http_headers{Name=VAR}
#           anything else (literal/mixed)        -> http_headers, the ${VAR}
#           refs expanded HERE at launch ($ENV); an unset ref stays literal,
#           matching claude's expansion semantics. Expanded values ride
#           codex's argv — equivalent exposure to the box env the agent
#           already reads; the by-name tiers exist so the common shapes
#           never even do that.
#
# Encoding: `-c` values parse as TOML. JSON string/array-of-string encoding
# is valid TOML for the same, and byre validation bans control characters in
# names/commands/urls/header templates, so jq's tojson output is line-safe
# and TOML-safe. Tables need TOML's `=` form (toml_table below) — JSON's
# colon object syntax is NOT valid TOML.
set -eu

# Overridable for tests (and hand-wiring experiments); byre boxes use the bake.
MCP=${BYRE_MCP_CONFIG:-/etc/byre/mcp.json}

flags=()
if [ -r "$MCP" ]; then
  while IFS= read -r line; do
    flags+=(-c "$line")
  done < <(jq -r '
    def expand: gsub("\\$\\{(?<n>[A-Za-z_][A-Za-z0-9_]*)\\}"; ($ENV[.n] // "${\(.n)}"));
    def toml_table: "{" + (to_entries | sort_by(.key) | map("\(.key|tojson) = \(.value|tojson)") | join(", ")) + "}";
    def env_ref: capture("^\\$\\{(?<n>[A-Za-z_][A-Za-z0-9_]*)\\}$").n;
    .mcpServers | to_entries | sort_by(.key)[] |
    if .value.url then
      .key as $k |
      ((.value.headers // {}) | to_entries | sort_by(.key)) as $hs |
      (($hs | map(select((.key | ascii_downcase) == "authorization" and (.value | test("^Bearer \\$\\{[A-Za-z_][A-Za-z0-9_]*\\}$")))))[0]) as $bearer |
      ($hs | map(select(($bearer != null and .key == $bearer.key) | not))) as $rest |
      ($rest | map(select(.value | test("^\\$\\{[A-Za-z_][A-Za-z0-9_]*\\}$")))) as $byname |
      ($rest | map(select((.value | test("^\\$\\{[A-Za-z_][A-Za-z0-9_]*\\}$")) | not))) as $lit |
      ( "mcp_servers.\($k).url=\(.value.url | tojson)",
        (if $bearer != null then
          "mcp_servers.\($k).bearer_token_env_var=\($bearer.value | ltrimstr("Bearer ") | env_ref | tojson)"
        else empty end),
        (if ($byname | length) > 0 then
          "mcp_servers.\($k).env_http_headers=\($byname | map({(.key): (.value | env_ref)}) | add | toml_table)"
        else empty end),
        (if ($lit | length) > 0 then
          "mcp_servers.\($k).http_headers=\($lit | map({(.key): (.value | expand)}) | add | toml_table)"
        else empty end)
      )
    else
      ( "mcp_servers.\(.key).command=\(.value.command | tojson)",
        "mcp_servers.\(.key).args=\(.value.args // [] | tojson)",
        (if ((.value.x_byre_env // []) | length) > 0 then
          "mcp_servers.\(.key).env_vars=\(.value.x_byre_env | tojson)"
        else empty end)
      )
    end
  ' "$MCP")
fi

# Agent context (ADR 0046): the baked file plus the launcher's per-session
# additions (BYRE_SESSION_CONTEXT arrives with its own leading separator, or
# empty/unset), injected as `developer_instructions` — codex emits it as a
# SEPARATE developer-role message, appended; base_instructions and AGENTS.md
# discovery untouched (source-verified: config_toml.rs developer_instructions,
# session/mod.rs developer_sections). The multi-line value fails TOML parse
# inside -c and falls back to a literal string by design (config_override.rs).
# byre writes NOTHING into $CODEX_HOME: its AGENTS.md is the user's file (the
# retired context_target rewrote it every launch).
CTX=${BYRE_AGENT_CONTEXT:-/etc/byre/agent-context.md}
ctx_text=""
[ -r "$CTX" ] && ctx_text="$(cat "$CTX")"
# The value rides ONE argv string; Linux caps a single exec argument at
# MAX_ARG_STRLEN (~128 KiB), under byre's 1 MiB context budget. A
# legal-but-large context must DEGRADE loudly, not kill the exec (grok
# review find, probed 2026-07-26): cap in BYTES (wc -c, not ${#} — that
# counts characters and multi-byte prose slips past; round 2) with a
# disclosure pointing at the baked file. Only the BAKED part is capped; the
# per-session additions (small) always append after, never silently dropped.
if [ "$(printf '%s' "$ctx_text" | wc -c)" -gt 100000 ]; then
  ctx_text="$(printf '%s' "$ctx_text" | head -c 100000)

[byre: instructions truncated at this agent's argv limit — full text: $CTX]"
fi
ctx_text="${ctx_text}${BYRE_SESSION_CONTEXT:-}"
if [ -n "$ctx_text" ]; then
  flags+=(-c "developer_instructions=$ctx_text")
fi

# ${flags[@]+...}: bash < 4.4 (macOS's 3.2 included, where the unit test
# runs this under the host bash) trips `set -u` expanding an EMPTY array.
exec codex ${flags[@]+"${flags[@]}"} "$@"
