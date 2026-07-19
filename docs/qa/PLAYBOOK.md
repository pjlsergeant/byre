# byre QA playbook

The standing journey suite for the release-time field-QA pass (see
RELEASING.md): per journey, the keystroke recipe, the screens to expect,
and what pass means. Each QA pass EXECUTES this playbook and EXTENDS it;
exploratory probing happens at the edges and graduates in here once
repeatable. Findings go to the "Open findings" section at the bottom of
this file, never fixed mid-pass; they leave it by being dispatched into
fixes + regression tests. Pass reports themselves are not kept here --
this file holds the procedure; git history holds the reports.

Recipes assume the sacrificial inttest VM, a fresh
`BYRE_HOME=$HOME/<qa>/home`, and the tmux vocabulary from
BYRE-DEVELOPMENT.md (`tmux -L <sock>`, capture with `grep -a` — TUI box
glyphs otherwise trip grep's binary heuristic).

## Conventions

- One tmux window per journey; kill boxes/volumes between journeys unless
  the journey needs residue.
- Dummy credentials only, except where a journey explicitly says a capped
  live key adds a liveness leg.
- Every recipe ends with TEARDOWN so residue never contaminates the next
  journey.

## Journey: opencode cold user

The full first-contact flow, wizard to working agent.

1. Fresh project dir + `BYRE_HOME=<qa>/home byre develop`.
   Expect: store notices naming the REAL home (never `~/.byre` under an
   override), then the wizard: `Template — go node python none [none]:`.
2. Enter (none) → `Agent — claude codex gemini grok opencode none [none]:`
   → type `opencode`, Enter.
   Expect: `Use machine-wide credentials to log in to opencode?
   [y/N, i for info]:` — bare line, NO provenance parenthetical (bundled
   claimant), no note line above.
3. `i`, Enter. Expect: the skill named with "(bundled with byre)", the
   machine-wide volume named, y/n write scopes, save-as-default's
   prefill-only effect. Question re-asked after.
4. `y` → `Save these as your default for new projects? [y/N]:` → `y`.
   Expect after build: exactly `skills = ["opencode-shared-auth"]` added
   to this project's byre.config; default.config gains `agent` +
   `shared_auth` favourites only.
5. Box launches into the firstrun login: "Pick a provider below; stored
   machine-wide (shared-auth: all your byre projects)."
   [liveness leg, capped key] provider → API key → paste → opencode TUI
   up, model line populated; a trivial prompt answers.
6. In-box: `auth.json` at the XDG data path is a SYMLINK into
   `/home/dev/.byre-identity/opencode/`; the shared store holds the entry.
7. SECOND fresh project: wizard prefills `[opencode]` and the offer
   `[Y/n]`; accept both → box comes up LOGGED IN, no prompt, no re-ask of
   save-default.
8. TEARDOWN: rm boxes, project state volumes, the identity volume; revoke
   any live key.

## Journey: MCP delivery to opencode

1. In a project with the opencode agent: append to its byre.config:
   `[[mcp]]` `name = "qa-probe"` `command = ["echo", "hi"]`.
2. `byre develop` (rebuild picks up the config).
   Expect in-box: `/etc/byre/mcp.json` carries qa-probe; the agent's
   PID 1 env carries `OPENCODE_CONFIG_CONTENT={"mcp":{"qa-probe":…}}`.
   (opencode's status line says "0 MCP" — that counts CONNECTED servers;
   an echo stub can't handshake. Not a failure.)
3. `byre status` from the project dir.
   Expect: `qa-probe — local: echo hi  (config)` and
   `-> the agent session receives: qa-probe  (injected via /etc/byre/mcp.json)`.
4. TEARDOWN: rm box.

## Journey: deliver flows

Exit codes per DELIVER.md. Two boxes running (A: cwd-owned, B: other
project).
1. `byre deliver <file>` from inside A's workdir → lands in A's /inbox,
   bytes exact; repeat same name → `-2` suffix, never clobbered.
2. `echo x | byre deliver` from a NEUTRAL dir, no tty →
   "2 boxes are running — pick one with --box" + candidates; exit 1.
3. Same, with a tty → picker opens; `q` → "cancelled — nothing
   delivered"; exit 1.
4. Same, pick a box with Enter → stdin lands as `stdin-<stamp>`; OSC 52
   clipboard note.
5. TEARDOWN: rm boxes.

## Journey: config UI, Claude Skills + dirty flag

1. `byre config` in a project → main form renders; `▸` cursor moves.
2. Down to `Claude Skills`, Enter → "(no items yet)"; `a` → two-field
   form. Junk NAME → immediate ✗ validation (message wraps at narrow
   widths). Valid name + nonexistent dir → live note
   "⚠ path missing — build will fail (accepted anyway…)"; accepting
   lists the row with the same warning suffix.
3. Esc → main shows `● Unsaved changes`; `^q` → discard needs a SECOND
   confirm; after discard the file on disk is byte-identical.
4. TEARDOWN: none (nothing saved).

## Journey: agent cold flows — claude / codex / grok

Per agent, fresh dir + wizard (`template` Enter, agent name, decline
save-default).
1. Vouched agents (claude, codex, opencode): expect the sharing question
   `Use machine-wide credentials to log in to <agent>? [y/N, i for info]:`
   — bare line. `i` → info text names the skill "(bundled with byre)",
   the volume, y/n write scopes, save-default's prefill-only effect.
   `y` → exactly `skills = ["<agent>-shared-auth"]` in the project config.
2. Unvouched companions (gemini, grok): expect NO sharing question —
   straight from Agent to save-default. (Flips when their skill gains
   `shared_auth_for`; update this recipe then.)
3. In-box firstrun: claude prompts for a setup-token paste, Enter skips
   ("byre: skipped — using this project's own login") — the paste prompt
   belongs to claude-shared-auth, so it appears ONLY with the skill
   enabled (sharing answered y, or the skill added to the store config);
   with sharing declined claude goes straight to its own onboarding.
   codex/grok run a device login, Ctrl-C skips (trap prints the
   byre-shell-later line — the agent's own alt-screen may repaint over
   it immediately; scrollback still shows it).
4. After any skip the agent shows its OWN onboarding/login — a skip gets
   a box, not a ready agent (informational, all agents).
5. Exits: gemini Ctrl-C at its login → exits 0, develop propagates; grok
   ctrl+q; claude's tmux-driven theme picker can wedge — if keys stop
   landing, `docker rm -f` the box (develop then reports the decoded
   `byre: exit status 137 (SIGKILL — the box was killed out from under
   the session: …)`, rc 1 — deliberate, ≥125 = engine range).
6. TEARDOWN: rm boxes + per-project volumes.

## Journey: seeded gemini — chooser must not appear

The 2026-07-16 field-failure regression check.
1. gemini-shared-auth is companion_for → not offered; hand-enable:
   `skills = ["gemini-shared-auth"]` in the STORE config
   (`$BYRE_HOME/projects/<slug>/byre.config`) — NOT a file at the project
   root (that's a preset and prints "not applied").
2. `byre develop`. Expect: jq + firstrun layers in the build; box up.
3. PASS = gemini goes STRAIGHT to the oauth-personal URL/code prompt — no
   auth-method chooser anywhere in `capture-pane -S` scrollback (contrast:
   a plain gemini box shows the chooser).
4. In-box: all four identity files in ~/.gemini are symlinks into
   /home/dev/.byre-identity/gemini (the machine volume, mounted);
   settings.json == {"security":{"auth":{"selectedType":"oauth-personal"}}}.
5. Garbage at the code prompt → invalid_grant + re-prompt (gemini's own
   handling); Ctrl-C → gemini exits 0.
6. TEARDOWN: rm box; keep or rm the machine identity volume deliberately.

## Journey: rude inputs

- Ctrl-C at the wizard: process dies on SIGINT, store gains NO config.
- Ctrl-C mid-build: buildx prints CANCELED/context canceled; develop
  exits 130; no stray containers; next develop skips onboarding and
  rebuilds clean. (Window is short on cached bases — use a fresh
  python/node project for an uncached pull.)
- Garbage at any y/N prompt (sharing question, save-default, reset/
  forget Proceed): reprompts with "unrecognized — y, n, …"; y/Y/n/N and
  i/I answer, Enter takes the default.
- Resize mid-wizard: line prompts rewrap, keep answering. Resize
  mid-config-UI: re-clips live, "··· (more below)" + footer intact.

## Journey: reset / forget / develop-while-running

1. Second `byre develop` while one runs: decline + how-to-reach text,
   rc=3 (ExitRefused — develop only; its exit code otherwise carries the
   agent's own status; reset/forget's decline-while-running stays rc=1,
   a deliberate asymmetry).
2. `byre reset` while a session runs: "a session is running … exit it
   before reset", rc=1. NEVER measure through a pipe — `cmd | tail`
   makes $? tail's; echo rc in a separate send (Ctrl-C also aborts the
   whole `cmd; echo rc=$?` line, so a compound never prints after an
   interrupt).
3. reset with the session down: kill-list enumerated with engine suffix
   `[docker]`, re-auth warning, default No; y → per-project volumes
   removed, machine-wide identity volumes NAMED as not-touched with the
   deliberate-delete path. rc=0.
4. forget: kill-list = image + store dir (config, marker, context); y →
   both gone; next develop re-onboards from the wizard. rc=0.
5. Orphaned box: develop in a private tmux server, wait for the in-box
   prompt, kill the whole tmux server. The container SURVIVES
   (deliberate — a crashed terminal must not kill the agent). Expect:
   `byre status` shows "running (…) — orphaned: the byre that started it
   is gone" naming `byre shell` and `<engine> stop <id>`; `byre reset`
   refuses with the same stop command appended. `docker stop <id>` then
   reset → normal kill-list. (Older boxes without the byre.client label
   just say "running".)
6. TEARDOWN: rm boxes + per-project volumes.

## Journey: worktrees

Needs a git repo with a commit; main project already developed, and the
box image must CARRY git (`apt = ["git"]` or a template that ships it) —
creation now runs `git worktree add` in a one-shot container on the
project image. A git-less image refuses loudly, naming the `byre config`
remedy, and creates nothing.
1. `byre worktree wt1 --path ../got-wt1` from the main tree.
   Expect: registration runs in-box, then develop starts IN the worktree
   and prints "populated the worktree checkout inside the box" (files
   appear at FIRST LAUNCH, not at create); image is the MAIN project's
   (no rebuild beyond cache); container slug from the worktree DIR name
   (`--path ../got-wt1` → `got-wt1-…`).
2. In-box: /workspace is the worktree, `git branch --show-current` works
   (worktree-metadata mount path).
2b. Stale-registration remedy: exit the wt session, `rm -rf` the worktree
   dir (registration stays), re-run the same `byre worktree`. Expect the
   targeted remedy naming `git -C <main> worktree prune` — never the
   engine-gate message and never a raw git error. (Pinned unit-side after
   the v1.1.0 macOS CI catch: recognition must compare git's RESOLVED
   path spelling even when the dir is gone.)
   The no-engine refusal itself can't be staged on the VM by stripping
   PATH — /bin is usr-merged into /usr/bin, so docker stays findable;
   macOS CI (engine-less runner) exercises that message instead.
3. Concurrent main-tree develop in another window: both boxes up.
4. `byre status` in the project: "Worktrees: 1 other session(s) live:
   <id> (share these volumes)".
5. deliver from the main tree: resolves to the cwd's OWN box, no picker
   (picker is for ambiguity); `deliver --box <wt-id>` lands in the
   worktree box's /inbox (verify bytes), labeled by the box's own
   workdir id ("delivering to <proj>-wt1-…"). status shows siblings the
   same way: "workdir-id (short-id)".
6. TEARDOWN: exit both; `git worktree remove` on the host if re-running.

## Journey: config UI ^e round-trip

1. `byre config` → `^e` → $EDITOR (vi) on the REAL store config.
2. Write an INVALID key (`packages = […]` — the Packages row's key is
   `apt`), :wq. Expect: UI keeps last-good values + red banner
   "✗ file has an error after editing (fix it and ctrl+e again): …
   unknown key(s): [packages]"; the file on disk DOES carry the bad edit.
3. `^e` again, remove the line, :wq → banner clears, "Reloaded from
   file". `^q` → "byre: config unchanged."
4. Pickers render `none` exactly once, whatever the config says.

## Journey: firewall egress

Run on `template = "none"` — the bare base is the regression-sensitive
case (language templates ship CA certs transitively and would mask it).

1. `skills = ["firewall"]`, no egress key → banner flips to
   "byre: network deny-by-default · egress none"; box still launches
   (gate opened = rules verified). curl anything → timeout, 000.
2. Add `egress = ["example.com"]` → banner "egress 1 host";
   `curl https://example.com` = 200 EVEN ON the none template (the
   skill ships ca-certificates with its diagnostic curl — a 77
   cert-verify error here is the trust-store regression, distinct from
   a block's timeout/000); everything else still times out.
3. TEARDOWN: rm box.

## Journey: templates + named layers

1. go/node/python templates: wizard-onboard each, box up. Toolchain on
   PATH in the box's LOGIN shell — `go version`, `node --version`,
   `python3 --version` in the agent=none foreground shell and via
   `byre shell`. (The login-shell leg matters: /etc/profile once
   clobbered the image ENV PATH; byre-env.sh restores it from the baked
   /etc/byre/image-path. If go vanishes again, compare with
   `docker exec <box> go version` — ENV intact there — to distinguish
   shim regression from a broken image.)
2. Layers: `byre layer new qa2base` → scaffold under $BYRE_HOME/layers
   (self-documenting comments; vocabulary = full config minus template).
   Add `apt = ["ripgrep"]` + `egress = ["example.com"]`; `byre layer
   validate qa2base` → ok. Project config gains `extends = "qa2base"` →
   next develop REBUILDS with rg baked in (`command -v rg`) and the
   layer's egress in the banner/probe. Edit the layer → next develop
   picks it up (live resolution).

## Journey: security-guard clobber note

On a project with a netns skill enabled (`skills = ["firewall"]`):
1. Add a `files` entry targeting a guarded path:
   `[files]` `"fake-launch" = "/usr/local/bin/byre-launch"` (any source
   file). `byre dockerfile` → stderr carries the note ("a `files` entry
   targets /usr/local/bin/byre-launch, a byre-managed security path …
   byre re-asserts its own copy at the build tail"); stdout stays the
   clean Dockerfile, whose tail shows the guard block re-COPYing the
   launcher, the launch gate, and the netns script before
   HEALTHCHECK/USER/ENTRYPOINT.
2. `byre develop` prints the same note; in the built box
   `/usr/local/bin/byre-launch` is byre's launcher (shebang + header),
   not the planted file — and the deny-by-default banner still holds.
3. TEARDOWN: remove the entry + rm box.

## Journey: Volumes screen scope grouping

With at least one project volume and one machine identity volume
existing: `byre config` → Volumes. Expect two groups — "Project
volumes" and "Machine volumes — shared by all your projects" — engine
suffix per row, and the state-volume explainer line at the bottom.

## To graduate (confirmed green in past passes, no recipe yet)

Write a recipe when a future pass covers one of these: host mounts +
store-edit apt; deliver of a directory; self-edit round-trip + exit
report (and self-edit's project-only store mount); skill fork; rehome
after `mv`; rebuild; docker-host containment-hole loudness;
forget --force (and invalid-config recovery through it); three-level
named-layer composition and project precedence; live layer-cycle
detection/recovery; canonical identity through a symlinked project path.

## Harness lessons (carry between passes)

- Never pipe the measured command when capturing an exit code, and never
  chain `; echo rc=$?` on a line you might Ctrl-C — send the echo as its
  own keystroke afterwards ($? survives until the next command).
- tmux `respawn-pane -k` RERUNS the window's original command — a window
  created with an inline `develop` relaunches the session. Create QA
  windows as bare shells and send commands with send-keys.
- Two Ctrl-Cs in ONE send-keys can arrive as one; send them as separate
  calls with a beat between, or expect TUIs (claude) to swallow them.
- Wizard answers race the prompts at fixed sleeps; capture the pane after
  each answer when a journey depends on WHICH question consumed a key.
- A cold Claude install is too slow for short automated passes; use an
  agent-less or pre-warmed box when the journey isn't about install.
- Non-TTY `develop` hangs at attach (expected) — always drive it under
  tmux/a real pty.
- Don't match a banner alone to conclude "box up" — wait-loop races have
  matched banner text before the `dev@` prompt and misreported hangs;
  wait for the prompt.
- Driving over ssh adds a THIRD shell layer: a `$?` or a quoted TOML
  string inside an ssh+send-keys wrapper gets expanded/stripped by the
  REMOTE shell before the keys land (observed: a cancel measured rc=0
  because the wrapper's own last status was echoed; a layer file landed
  quoteless and byre rightly refused it). Send `echo rc=$?`
  single-quoted end to end, and write config/layer files via ssh
  heredoc, never typed keys.
- ONE driver per VM at a time. The gated suite assumes an exclusive
  engine: a concurrent QA pass's boxes walk into the deliver pool-scan
  tests ("want exactly this box") and fail them. Before starting a suite
  run or a pass, check for a live driver: `tmux ls` on every socket you
  know (`tmux -L <sock> ls`), and `docker ps --filter
  label=byre.project` for boxes you don't own.
- /bin is usr-merged on the VM, so PATH-stripping cannot hide docker for
  a no-engine leg; an engine-less environment (e.g. macOS CI) exercises
  those messages instead.
- gemini's oauth code prompt times out after 5 minutes of inactivity
  (its own limit, exit 41) — drive it promptly.
- The opencode shared-auth firstrun gate re-runs on EVERY launch until a
  login exists, so a loginless box must be skipped past the gate before
  probing agent env.

## Open findings

Report-only pass findings awaiting dispatch — live bug reports and
design questions are handled in-session and tracked in TODO.md when
parked, not here. Dispatched findings are removed (fix + regression
test; the recipes above assert fixed behavior); git history keeps the
reports.

- **^e quit says "wrote" with nothing to write** (found 2026-07-18,
  widened 2026-07-19): after an external edit + "Reloaded from file"
  with NO unsaved changes, `^q` prints `byre: wrote <path>` — a
  content-no-op write where the recipe says `byre: config unchanged.`
  First seen on a never-developed project (the enrollment-at-open
  trade-off), since reproduced on a fully enrolled one, so the
  no-op-write applies generally. Content verified identical after the
  write. Legibility only.
- **Worktree create double-prints two messages** (found 2026-07-19): the
  git-less-image failure ("creating the worktree in the box failed:
  exit status 1") and the success line ("populated the worktree
  checkout inside the box.") each print twice per run. Legibility only.
- **Wizard abort leaves an enrolled husk** (found 2026-07-18, note):
  Ctrl-C at the template prompt leaves `projects/<id>/` holding path
  record + context dir, no config. Same class as the consciously-accepted
  reset/forget abort-enrollment stance (2026-07-17); recorded so the
  next pass doesn't re-discover it.
