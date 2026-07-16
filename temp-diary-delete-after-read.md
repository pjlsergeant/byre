# Handover diary — OpenCode agent build (2026-07-16)

DELETE AFTER READING. Session notes from the container session that built the
`opencode` + `opencode-shared-auth` skill pair on branch
`claude/opencode-agent-build-4hj1of` (6 commits). Everything durable is in the
shipped docs/skill comments; this file is the stuff that's only in my head,
plus a map of where the durable stuff landed.

## What shipped, in one breath

TODO's OpenCode item, both halves: facts established empirically first
(recorded in `docs/AGENT-CREDENTIAL-MECHANICS.md`, new OpenCode section +
summary-table column + implications entry), then the skill pair per the grok
playbook. TODO.md now carries only the residual host gates. Docs sweep done
(README, GLOSSARY, ARCHITECTURE, site/how-do-i, site/skills-and-templates all
say five agents now). Full review pass ran (8 finder angles + verify); 8
findings fixed in the last commit, 2 consciously deferred (below).

## The environment I probed in — read this before trusting/extending the facts

- A Claude Code remote container, NOT a byre box, NOT the usual dogfood env.
  Network was deny-by-default through a proxy: **opencode.ai and models.dev
  were BLOCKED; registry.npmjs.org was open.** That shaped everything:
  - I installed opencode 1.18.2 from npm (`opencode-linux-x64` platform pkg —
    same binary the installer ships) instead of the official installer.
  - The blocked models.dev turned out to be a FINDING, not just a nuisance:
    opencode's provider catalog comes from models.dev at runtime, and with it
    blocked the `auth login` picker silently degrades to API-key-only (the
    "Login with Claude Pro/Max" option never appears) and the default-model
    pick falls back to an embedded snapshot (it picked xai/grok-3-mini
    offline). Hence models.dev in the skill's functional egress.
  - sst/opencode on GitHub was out of this session's repo scope, so "source"
    claims in the doc mean **JS embedded in the shipped Bun binary** (readable
    via `strings`, minified) + the vendor's published `opencode-anthropic-auth`
    npm package (0.0.13 — the compiled-in flow's published ancestor). The
    binary may hold bytecode chunks strings can't see; the doc says so. When
    someone has normal GitHub access, re-verify against real source.

## Empirical results you'd otherwise have to redo

All in the doc, but the load-bearing ones:

- `opencode debug paths` is the state-dir oracle (they ship introspection —
  use it, don't infer). XDG split: data (auth.json + opencode.db + log +
  repos) / config / cache / state. `XDG_DATA_HOME` and `XDG_CONFIG_HOME`
  relocations VERIFIED live. `OPENCODE_CONFIG_DIR` is flaky — one code path
  honors it, `debug paths` ignores it (1.18.2). Don't build on it.
- **Gate 1 of shared-auth passed live in this session**: I ran a real
  `opencode auth login` (API-key path, fake key) with `auth.json` pre-linked
  to another directory — the write went THROUGH the symlink, same inode,
  chmod 600 on the target. In-place `writeJson` confirmed in binary source
  (`Auth.set` → `FileSystem.writeJson` → `writeFileString`, no temp+rename).
  Codex-shaped, not grok-shaped.
- Headless `opencode run` with a permission "ask": prints
  "permission requested: …; auto-rejecting" and REJECTS — never hangs (the
  grok silent-death lesson doesn't apply). `--auto` (hidden aliases `--yolo`,
  `--dangerously-skip-permissions`) flips to approve-once. Default `build`
  agent ruleset already opens `{permission:"*", action:"allow"}` with
  ask-carve-outs for doom_loop/external_directory and deny for
  question/plan_enter (`opencode debug agent build` shows it).
- auth.json is a provider-keyed MULTI-credential store; every entry carries a
  `"type"` member (api | oauth | wellknown). The login-hook guard rides that
  shape + a trailing-`}` check (in-place writes mean truncated files are
  real; a bare `-s` + grep passes a truncation that got past the type token).
- Anthropic Pro/Max OAuth endpoints (claude.ai authorize,
  console.anthropic.com token/refresh) are NOT in the binary as plain
  strings — they come from the vendor plugin lineage. Evidence chain is in
  the doc's source list.

## HOST GATES — the actual remaining work (now the TODO item)

Run these from a real machine with Docker; none were possible here:

1. **Installer path**: `byre develop` with `agent = "opencode"`. The skill
   assumes `~/.opencode/bin/opencode`; if upstream moved it, the build fails
   at the `test -x` line — fix the path, don't guess. Also confirm whether
   the installer actually needs `unzip` (shipped defensively; drop it if
   not — review finding deferred on exactly this).
2. **Pro/Max login**: first launch, firstrun hook fires `opencode auth login`
   → pick Anthropic → confirm the paste-code flow completes browserless
   in-box and lands in the state volume. Needs models.dev + claude.ai +
   console.anthropic.com egress if firewalled.
3. **`--auto` in the live TUI** and general session sanity.
4. **Firewalled egress set**: run with the firewall skill, watch for denied
   hosts I didn't predict.
5. **opencode-shared-auth rotation gate (gate 2)**: enable the companion,
   log in once (Pro/Max OAuth), run TWO boxes concurrently past a token
   expiry (~hours), confirm neither dies. Anthropic refresh tokens are
   single-use server-side (same infra as the Claude Code cascade-logout
   issues), so this is the genuinely uncertain one. If it passes, add
   `shared_auth_for = "opencode"` to the companion (that flips the ADR 0025
   onboarding offer on) and update: companion skill.toml STATUS block, the
   `TestBuiltinSharedAuthDeclarations` map in skills_test.go, the
   AGENT-CREDENTIAL-MECHANICS OpenCode §3/§5, ARCHITECTURE's gate-pending
   sentence. If it fails, the grok retirement (ADR 0023) is the template.
6. Then strip EXPERIMENTAL from the opencode skill description.

## Things I know that are NOT fully recorded elsewhere

- **codex-login.sh has the same loose carve-out I fixed in opencode's hook.**
  Its case pattern trusts links into ANY `/home/dev/.byre-identity/*` subdir,
  so a planted link from codex's auth.json into e.g. grok's identity dir
  would be kept (and codex would write through it). I fixed opencode to an
  equality check on its OWN identity dir but did NOT touch codex (out of
  scope for this branch). Worth a small follow-up commit on main.
- The review pass reproduced the cross-agent clobber in simulation before
  the fix (opencode login overwrote a codex-shaped shared credential through
  a planted link) and re-ran it after (link removed as foreign, codex file
  untouched). The simulation lived in the container scratchpad — gone with
  the container; trivially re-creatable from the description in the
  review-fixes commit message.
- **`OPENCODE_AUTH_CONTENT`** env injects the entire auth store (static —
  refresh can't write back). Not used by the skills, but it's the cleanest
  future seam for a token-env mechanism à la claude-shared-auth if the file
  gate fails. Also noteworthy: `OPENCODE_PERMISSION` (JSON merged into
  permission config) as an alternative to `--auto` if finer grain is wanted.
- **MCP seam** (ADR 0033 probe, recorded in skill.toml but tersely):
  `OPENCODE_CONFIG` (extra config file path) and `OPENCODE_CONFIG_CONTENT`
  (config JSON via env) both exist in 1.18.2, and config carries an `mcp`
  block. What I did NOT pin: whether injected config MERGES with or REPLACES
  user config, and the same-name shadowing behavior (the question the claude
  adapter settled by spike, 2026-07-15). Spike that before any
  `mcp = "inject"` vouch.
- The four failing tests in `internal/commands` (onboard/preset write-failure
  sims) are PRE-EXISTING and environmental: they chmod files to simulate
  unreadability and this container runs as root, which ignores modes.
  Verified identical on a clean main checkout. Not my diff.
- Deferred review findings (conscious, per the fix-or-defer rule):
  (a) parameterizing the shared-auth hook/composition test suites across
  codex+opencode+gemini — house style so far is per-agent copies; touching
  codex tests felt out of scope here, but the third repetition is a real
  signal if a fourth agent lands; (b) folding the `rm -rf` state-dir cleanup
  into the installer RUN (layer-whiteout pedantry; claude/gemini share the
  pattern) and the `unzip` question — both parked on the host-gate item.
- Commit granularity on the branch: doc → agent skill → companion → tests →
  docs sweep → review fixes. The review-fixes commit message doubles as the
  findings record.

## Trip hazards for whoever picks this up

- Don't "fix" the volume target: `.opencode` volume mounts at
  `/home/dev/.local/share/opencode` (the DATA dir), deliberately NOT
  `~/.opencode` (binary dir, image-side). Precedent: codex's `.codex` volume
  mounts at `.codex-home`, not `.codex`.
- Don't remove the `gosu dev mkdir -p /home/dev/.local/...` dockerfile line:
  gen's volume-mount layer chowns only the LEAF, and opencode is the first
  agent whose target nests two levels under $HOME — without the pre-create,
  `~/.local` and `~/.local/share` bake root-owned and opencode can't make
  its lock dir at runtime.
- Don't re-add an env seam (BYRE_IDENTITY_BASE or otherwise) to the login
  hook's trusted-dir check "for testability" — firstrun hooks inherit
  container env, config can set [env], and that's exactly the redirection
  hole the hardcode closes. The carve-out being unit-untestable is priced in
  (codex's is untested too); the shared-auth hook keeps its seams because
  the login hook is the backstop.
- The XDG_DATA_HOME honoring in both hooks is hooks-follow-CLI coherence,
  not a byre knob. If a user relocates it via [env], state leaves the volume
  (skill.toml warns). Don't pin XDG_DATA_HOME box-wide to "fix" this — it
  would drag every XDG tool's data into the state volume.
