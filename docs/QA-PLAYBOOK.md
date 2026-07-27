# byre QA playbook

The standing journey suite for the release-time field-QA pass (see
RELEASING.md): per journey, the keystroke recipe, the screens to expect,
and what pass means. Each QA pass EXECUTES this playbook and EXTENDS it;
exploratory probing happens at the edges and graduates in here once
repeatable. Findings are never fixed mid-pass: they go to TODO.md, and
leave it by being dispatched into fixes plus regression tests, which the
recipes here then assert. This file holds the procedure only -- pass
reports and dispatched findings live in git history and TODO.md.

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

## Journey: grab flows

deliver's mirror; exit codes per DELIVER.md. Needs two boxes running (A:
cwd-owned, B: other project).

1. Plant a file in A's box (`docker exec … sh -c 'printf x > /workspace/out.txt'`);
   `byre grab /workspace/out.txt` from A's workdir → lands in the cwd,
   bytes exact; repeat → `-2` suffix, never clobbered.
2. Directory: plant `qa-dir/` holding two files in subdirs, a symlink and
   a FIFO. `byre grab /workspace/qa-dir` → regular files land with the
   tree shape; the symlink and FIFO each get a loud
   "skipping … (not a regular file or directory)"; summary counts files
   + bytes; rc=0.
3. From a NEUTRAL dir, no tty → "2 boxes are running — pick one with
   --box" + candidates; rc=1.
4. Missing box path → "no such path in the box: <path>", rc=1.
5. TEARDOWN: rm boxes, rm the landed files.

## Journey: standing instructions ([[context]])

No engine needed until step 5.

1. `byre context add lint --text "Always run gofmt."` → "added" + the
   applies-next-develop note. Bare `add lint` with EDITOR exiting
   unchanged → "context lint unchanged." rc=0. Empty editor on a NEW
   name → "no text written — nothing added" (no remove hint); on an
   EXISTING name → the remove hint, rc=1 both.
2. Same name at two layers (`--global` then project) → `context list`
   attributes: `lint  "…"  (project — overrides default)`; a
   project-only snippet shows `(project)`. `context remove` of an
   inherited-only name → closure written (`"!name"`), list shows the
   shadow row `name  — removed by project  (was default)`.
3. `add notes --file <missing>` → accepted WITH the ⚠ does-not-exist
   warning; the next develop fails loudly naming context + file.
4. Size tiers (develop-time, both forms): ≥100 KiB note, ≥500 KiB ⚠,
   ≥1 MiB 🛑 suggesting a skill — develop PROCEEDS through all three; a
   >16 MiB file refuses with "not agent-memory-sized" (fstat-judged; a
   sparse file stages it cheaply).
5. develop (agent none suffices): /etc/byre/agent-context.md carries the
   merged prose, cascade order (layer before project), after byre's own
   and the skills' context.
6. TUI: `byre config` → Instructions → rows attributed `(layer:x)`;
   `d` on an inherited row → "removed here" + Restore action; item
   editor `^e` prose round-trip returns with the edited text shown.
7. TEARDOWN: rm box + store.

## Journey: exit report

The report names changes in
the places the HOST runs code from that `git diff` cannot show you. Its
survival condition is SILENCE, so the quiet leg matters more than the
loud one.

PREREQ: the box image must carry git (`apt = ["git"]` in the STORE
config) -- the `none` template has none, and a git-less box makes every
leg vacuous rather than failing (observed: a whole leg passed for the
wrong reason). Fresh git repo with a commit and a `.env`.

1. **Quiet leg (the one that matters).** In-box: commit some work,
   `git config remote.origin.url …`, `git config branch.main.remote …`,
   `git config filter.lfs.smudge "git-lfs smudge -- %f"`. Exit.
   PASS = NO report at all. Any output here is the wallpaper failure the
   ranking exists to prevent.
2. **Hooks.** In-box `date > .git/hooks/pre-commit; chmod +x` → exit.
   Expect `⚠ we thought you should know …` + `(byre checks a handful of
   places, not everything)` then
   `.git/hooks/pre-commit was added -- your git runs this, on your machine`.
   Append to it next session → `changed`, same suffix.
3. **Config, value shown vs withheld.** `git config core.hooksPath
   .husky/_` → `core.hookspath is set to .husky/_` (path-like: the
   destination IS the message). `git config credential.helper
   XhelperSECRETX` → `credential.helper was set` and the value must NOT
   appear -- and the verb must not dangle ("is set to" with nothing
   after it).
4. **Key userinfo redaction.** `git config
   url.https://tok3nSECRET@example.com/.insteadOf git@example.com:` →
   `url.https://<redacted>@example.com/.insteadof was set`. The token
   string must be absent from the pane. (Secrets live in the KEY for
   this shape; disable bash history expansion with `set +H` first or
   `!`-bearing values die as "event not found".)
5. **Env keys, names only.** Rewrite `.env` changing one key and adding
   another → `.env: added NODE_OPTIONS` / `.env: changed DATABASE_URL`.
   No VALUE may appear anywhere in the pane. `.envrc` is NOT watched
   (direnv gates it itself) -- writing one must stay silent.
6. **Nonzero exit still reports.** With something changed, `exit 3` →
   the report still prints AND develop propagates rc=3.
7. TEARDOWN: rm box + project volumes + image; rm the QA project.

## Journey: config UI, Claude Skills + dirty flag

1. `byre config` in a project → main form renders; `▸` cursor moves.
2. Down to `Claude Skills`, Enter → "(none yet)"; `a` → two-field
   form. Junk NAME → the rule line shows live; ✗ validation renders on
   the accept attempt (Enter). Valid name + nonexistent dir → live note
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

## Journey: config UI, env_from_host scheme picker

1. Project config `agent = "none"` (byre ships six passthroughs, so the
   screen always has rows). `byre config` → ↓↓↓ Enter. Expect the explainer
   above the rows, six `KEY <- host <scheme>  (byre default)` rows, and the
   exposure line matching the row count.
2. Enter on a row → `Set in: byre default` + `Override here`. Enter → the
   picker renders FIRST with the row's current scheme highlighted (prove
   with `capture-pane -e`, not plain text) and the argument label matching.
3. ←/→ across all five schemes: the label column must not change width, and
   the argument's placeholder explains schemes that take no argument. From
   `[disabled]` one more → wraps to `[value]`.
4. Scheme `value` on a key that is also a passthrough → an `[env]` literal
   row PLUS the passthrough row annotated `(… — overridden by [env], not
   passed)`; the counts must not move (one key, one grant — the field
   summary counts distinct keys, and must equal the exposure line).
5. Add a NEW key with scheme `env:` → the row appears and both counts rise
   BEFORE any save. (2026-07-27: it did neither; the entry was written to
   disk invisibly and a second add answered `✗ duplicate key`.)
6. Set an inherited key to `disabled` → the row reads `KEY <- disabled`,
   drops out of both counts; save, quit, reopen → the key is still visible
   and re-enableable from the screen. (2026-07-27: the row vanished
   entirely, TUI-unreachable.)
7. Rude keys — `BAD KEY`, `1STARTS_WITH_DIGIT`, `ünïcödé`, `K=EQUALS`,
   `BYRE_EGRESS` — each rejected naming the rule that fired; `git:` with
   `user name with spaces` rejected as an invalid git config key.
8. TEARDOWN: none (discard).

## Journey: config UI, Build files

1. `byre config` → ADVANCED → Build files. Expect the one-line explainer
   above the rows and `+ add Build files`.
2. Refusals, each naming its rule: absolute source; relative destination;
   empty source; empty destination; whitespace-only source; a duplicate
   source already listed; `../../../etc/shadow` → "escapes the project dir"
   (2026-07-27: the editor accepted it and the build refused later — editor
   and `byre dockerfile` must agree).
3. A source that does not exist: live note `⚠ source not in the project —
   build will fail (accepted anyway…)`, the Claude Skills affordance. Row
   accepted; `byre dockerfile` then refuses naming the entry and the remedy
   ("create it, or remove the entry"), never a raw lstat error.
4. Valid entry + a real file → `byre develop` (agent none, template none) →
   the file is at the destination in the box, and `byre dockerfile` shows
   `COPY files/<src> <dest>` before the guard block.
5. Destination `/usr/local/bin/byre-launch` with `skills = ["firewall"]` →
   the clobber note on stderr and the guard block re-COPYing the launcher
   at the tail (see the security-guard journey).
6. TEARDOWN: rm box + image; delete the entries.

## Probe: hostile config strings

Load a config whose values carry TOML `\r` and `\u001b` escapes in `[env]`,
`[files]` and mount targets (literal control bytes never parse; the escape
form is the live path), then read every config-UI screen with
`capture-pane | cat -v` and `capture-pane -e`. No value may move the
cursor, overwrite a row, or emit SGR that outlives its own row; the
printable payload text must survive, stripped, on its own row. (2026-07-27:
`\r` forged a row and hid the real one; an SGR escape ended byre's styling
mid-row. `byre status` was unaffected — key names only, grammar-refused.)
Residual, deliberate: the item editor's prefill and the raw-block textarea
render through bubbletea widgets and do not strip — editing surfaces show
the raw truth.

## Journey: Volumes screen scope grouping

With at least one project volume and one machine identity volume
existing: `byre config` → Volumes. Expect two groups — "Project
volumes" and "Machine volumes — shared by all your projects" — engine
suffix per row, and the state-volume explainer line at the bottom.

## Journey: shared-auth field gates — gemini + grok (PARKED — live logins + maintainer)

Not part of a normal pass: two live checks, each needing the maintainer
and a real login host-side (an exception to the dummy-creds convention).
Each flips `companion_for` -> `shared_auth_for` in the skill's skill.toml
on pass (plus the `TestBuiltinSharedAuthDeclarations` table and the
skill's composition pin test). A vouch follows its field gate, never
source alone (the grok-v1 lesson). Tracked in TODO.md ("Maybe someday").

1. **gemini two-box OAuth:** two boxes with gemini + gemini-shared-auth.
   Box A: real "Login with Google" paste-code flow — the seeded
   `selectedType` means NO auth-method picker appears (if it does,
   that's a finding); after login, `~/.gemini/oauth_creds.json` must
   still be a SYMLINK into `~/.byre-identity/gemini/` (a regular file =
   the login-fork came back). Box B, launched after: `gemini -p 'say ok'`
   with no login prompt. GOTCHA: do not open gemini's `/auth` dialog
   after login — it rm's the symlink and re-forks. Rotation is already
   proven safe (Google installed-app refresh tokens don't rotate;
   AGENT-CREDENTIAL-MECHANICS, Gemini §3), and the seed plumbing was
   field-proven credential-less in QA pass #2 — only the live cross-box
   login remains.
2. **grok ~6h broker rollover (ADR 0036):** watch a real box refresh
   through the broker across the access-token lifetime — or force it
   (the broker honors `GROK_AUTH_EXPIRED=1`; see
   `grok-shared-auth/grok-auth-broker.sh`) — and confirm the backend
   accepts the refreshed pair end to end.

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
- gemini's auth chooser and code prompt each swallow a lone Ctrl-C
  (2026-07-22: a "seeded box still shows the chooser" scare was the OLD
  plain box still alive because its Ctrl-C never landed). Send Ctrl-C
  twice as separate calls, then VERIFY the session died (docker ps)
  before treating later screens as the new box's.
- Stale pane frames satisfy wait-loops: a "dev@"/banner match can hit
  the PREVIOUS session's scrollback and misreport the new one as up.
  `clear` + `tmux clear-history` before a relaunch you intend to wait
  on, or match against `tail -1` of the pane, not the whole capture.
- The opencode shared-auth firstrun gate re-runs on EVERY launch until a
  login exists, so a loginless box must be skipped past the gate before
  probing agent env.
- A bare `byre-inttest` can report a green gate it did not run: go test
  replays CACHED package results, including the docker-touching ones
  (`internal/runner (cached)` on a suite that builds boxes). For a release
  gate always pass `-count=1`, and prove the tier fired at least once per
  pass with `-v`: count `=== RUN` and require zero `SKIP` (a missing
  `BYRE_DOCKER_TESTS`/`BYRE_TUI_TESTS` skips silently and still prints
  `ok`).
- Driving `vi` through `^e`: `o` on a COMMENT line inherits vim's comment
  leader, so an intended invalid-key edit lands as `# packages = [...]` --
  valid config, and the UI correctly says "Reloaded from file". A journey
  step expecting the error banner then passes for the wrong reason.
  Position on a non-comment line first (`:N`, then `o`), and always `cat`
  the file to confirm the edit you think you made.
- `byre: wrote <path>` on `^q` after a `^e` edit is NOT a spurious save:
  reportSaved compares open-time and quit-time BYTES, so a lasting external
  edit reports written and a net-identical round trip reports unchanged.
  The "config unchanged" step in the `^e` journey holds only because its
  round-trip is net-identical -- don't file the other case as a bug.
- After a Ctrl-C the tmux wrapper's `; echo $? > file` never runs, so "rc
  file still empty" proves nothing about whether the process died. Judge
  death by `tmux list-panes -F '#{pane_dead}'` and a `pgrep`, not by the
  status file -- and re-test a "the first Ctrl-C was ignored" impression
  before reporting it (one did not reproduce, 2026-07-27).
