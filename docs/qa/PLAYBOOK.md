# byre QA playbook

The standing journey suite for the release-time field-QA pass (see
RELEASING.md): per journey, the keystroke recipe, the screens to expect,
and what pass means. Each QA pass EXECUTES this playbook and EXTENDS it;
exploratory probing happens at the edges and graduates in here once
repeatable. Findings go to the "Open findings" section at the bottom of
this file, never fixed mid-pass; they leave it by being dispatched into
fixes + regression tests. (This file absorbed the pass #1/#2 charter,
2026-07-17 — pass-1's five findings shipped as the Unit-1 fixes,
pass-2's six on the same day; git history keeps the full reports.)

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

## Journey: opencode cold user (pass #1, 2026-07-17 — PASSED end to end)

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

## Journey: MCP delivery to opencode (pass #1 — PASSED)

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

## Journey: deliver flows (pass #1 — PASSED; exit codes per DELIVER.md)

Two boxes running (A: cwd-owned, B: other project).
1. `byre deliver <file>` from inside A's workdir → lands in A's /inbox,
   bytes exact; repeat same name → `-2` suffix, never clobbered.
2. `echo x | byre deliver` from a NEUTRAL dir, no tty →
   "2 boxes are running — pick one with --box" + candidates; exit 1.
3. Same, with a tty → picker opens; `q` → "cancelled — nothing
   delivered"; exit 1.
4. Same, pick a box with Enter → stdin lands as `stdin-<stamp>`; OSC 52
   clipboard note.
5. TEARDOWN: rm boxes.

## Journey: config UI, Claude Skills + dirty flag (pass #1 — PASSED)

1. `byre config` in a project → main form renders; `▸` cursor moves.
2. Down to `Claude Skills`, Enter → "(no items yet)"; `a` → two-field
   form. Junk NAME → immediate ✗ validation (message wraps at narrow
   widths). Valid name + nonexistent dir → live note
   "⚠ path missing — build will fail (accepted anyway…)"; accepting
   lists the row with the same warning suffix.
3. Esc → main shows `● Unsaved changes`; `^q` → discard needs a SECOND
   confirm; after discard the file on disk is byte-identical.
4. TEARDOWN: none (nothing saved).

## Journey: agent cold flows — claude / codex / grok (pass #2 — PASSED)

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
   ("byre: skipped — using this project's own login"); codex/grok run a
   device login, Ctrl-C skips (trap prints the byre-shell-later line —
   grok's own alt-screen may repaint over it immediately).
4. After any skip the agent shows its OWN onboarding/login — a skip gets
   a box, not a ready agent (informational, all agents).
5. Exits: gemini Ctrl-C at its login → exits 0, develop propagates; grok
   ctrl+q; claude's tmux-driven theme picker can wedge — if keys stop
   landing, `docker rm -f` the box (develop then reports the decoded
   `byre: exit status 137 (SIGKILL — the box was killed out from under
   the session: …)`, rc 1 — deliberate, ≥125 = engine range).
6. TEARDOWN: rm boxes + per-project volumes.

## Journey: seeded gemini — chooser must not appear (pass #2 — PASSED)

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

## Journey: rude inputs (pass #2 — PASSED; garbage-decline finding fixed)

- Ctrl-C at the wizard: process dies on SIGINT, store gains NO config.
- Ctrl-C mid-build: buildx prints CANCELED/context canceled; develop
  exits 130; no stray containers; next develop skips onboarding and
  rebuilds clean. (Window is short on cached bases — use a fresh
  python/node project for an uncached pull.)
- Garbage at any y/N prompt (sharing question, save-default, reset/
  forget Proceed): reprompts with "unrecognized — y, n, …"; y/Y/n/N and
  i/I answer, Enter takes the default. (Pass-2 found the silent-decline;
  fixed same day.)
- Resize mid-wizard: line prompts rewrap, keep answering. Resize
  mid-config-UI: re-clips live, "··· (more below)" + footer intact.

## Journey: reset / forget / develop-while-running (pass #2 — PASSED)

1. Second `byre develop` while one runs: decline + how-to-reach text,
   rc=3 (ExitRefused — develop only; its exit code otherwise carries the
   agent's own status).
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
5. Orphaned box (codex pass finding, fixed 2026-07-17): develop in a
   private tmux server, wait for the in-box prompt, kill the whole tmux
   server. The container SURVIVES (deliberate — a crashed terminal must
   not kill the agent). Expect: `byre status` shows "running (…) —
   orphaned: the byre that started it is gone" naming `byre shell` and
   `<engine> stop <id>`; `byre reset` refuses with the same stop
   command appended. `docker stop <id>` then reset → normal kill-list.
   (Older boxes without the byre.client label just say "running".)

## Journey: worktrees (pass #2 — PASSED)

Needs a git repo with a commit; main project already developed.
1. `byre worktree wt1 --path ../got-wt1` from the main tree.
   Expect: worktree + branch wt1 created; develop starts IN it; image is
   the MAIN project's (no rebuild beyond cache); container slug
   `<proj>-wt1-…`.
2. In-box: /workspace is the worktree, `git branch --show-current` works
   (worktree-metadata mount path).
3. Concurrent main-tree develop in another window: both boxes up.
4. `byre status` in the project: "Worktrees: 1 other session(s) live:
   <id> (share these volumes)".
5. deliver from the main tree: resolves to the cwd's OWN box, no picker
   (picker is for ambiguity); `deliver --box <wt-id>` lands in the
   worktree box's /inbox (verify bytes), labeled by the box's own
   workdir id ("delivering to <proj>-wt1-…"). status shows siblings the
   same way: "workdir-id (short-id)". (Both were bare/project-labeled —
   pass-2 findings, fixed same day.)
6. TEARDOWN: exit both; `git worktree remove` on the host if re-running.

## Journey: config UI ^e round-trip (pass #2 — PASSED)

1. `byre config` → `^e` → $EDITOR (vi) on the REAL store config.
2. Write an INVALID key (`packages = […]` — the Packages row's key is
   `apt`), :wq. Expect: UI keeps last-good values + red banner
   "✗ file has an error after editing (fix it and ctrl+e again): …
   unknown key(s): [packages]"; the file on disk DOES carry the bad edit.
3. `^e` again, remove the line, :wq → banner clears, "Reloaded from
   file". `^q` → "byre: config unchanged."
4. Pickers render `none` exactly once, whatever the config says
   (pass-2's double-[none] on agent="none", fixed same day).

## Journey: firewall egress (pass #2 — PASSED)

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

## Journey: templates + named layers (pass #2 — PASSED, one bug)

1. go/node/python templates: wizard-onboard each, box up. Toolchain on
   PATH in the box's LOGIN shell — `go version`, `node --version`,
   `python3 --version` in the agent=none foreground shell and via
   `byre shell`. (go was pass-2's headline bug: /etc/profile clobbered
   the image ENV PATH; byre-env.sh now restores it from the baked
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
- Don't match a banner alone to conclude "box up" — early "firewall
  hung" reports were wait-loop races matching banner text before the
  `dev@` prompt; wait for the prompt (grok explore pass, 2026-07-17).
- ONE driver per VM at a time. The gated suite assumes an exclusive
  engine: a concurrent QA pass's boxes walk into the deliver pool-scan
  tests ("want exactly this box") and fail them — diagnosed 2026-07-17
  when a pass's deliberately-orphaned box photobombed two suite runs.
  Before starting a suite run or a pass, check for a live driver:
  `tmux ls` on every socket you know (`tmux -L <sock> ls`), and
  `docker ps --filter label=byre.project` for boxes you don't own.

## Open findings

From dogfooding the gussy-up-gui session (2026-07-17, Pete; absorbed
from wip/gussy-up-gui-handoff.md, deleted):

1. (product, worktree lifecycle) Removing/pruning a worktree host-side
   while its box is live breaks every git command in the box ("not a
   git repository: …/worktrees/<name>"): the box mounts the main
   `.git`, whose `worktrees/<name>` metadata just vanished. Verified
   in-box repair (additive, reversible): recreate
   `.git/worktrees/<name>/{HEAD,commondir,gitdir}` using HOST paths
   (both mounts sit at host-true paths in the box, so they resolve),
   then `git read-tree HEAD` to rebuild the index; host-side
   `git worktree repair` also works. Open question pending dispatch:
   should byre detect/handle this, or document the repair?

2. (product, live-report 2026-07-17, Pete) Config UI Volumes section
   dies wholesale when ANY installed engine is unreachable:
   volumeAdmin.List returns on the first per-engine error, so podman
   installed with its machine stopped (macOS: exit 125, "Cannot
   connect to Podman … podman.sock: no such file or directory") kills
   the whole section including docker's listable rows. Repro: box with
   docker running + podman installed, podman machine stopped, byre
   config → Volumes. Proposed fix pending dispatch: per-engine degrade
   — unreachable engine contributes no rows plus a loud section note
   ("podman unreachable — its copies aren't shown and can't be cleared
   this session"), reachable engines list normally; deliver's
   partial-pool posture applied to the volumes view (needs a small
   configui.VolumeAdmin interface change for section notes).

Previously closed: the codex lifecycle/config pass's finding (2026-07-17: client
hangup orphans a running box — kill the tmux pane/server and the
container survives, executable; `status` said only "running" and
`reset --force` refused with no way out but raw `docker stop`) was
dispatched the same day as legibility, deliberately NOT kill-on-hangup:
a box surviving a crashed terminal keeps the agent alive, which is a
feature once labeled. Boxes now carry a `byre.client=<pid>` label;
`status` probes it and renders "running (…) — orphaned: the byre that
started it is gone…" with both routes out (`byre shell`, `<engine>
stop <id>`), and the reset/forget session-running refusals carry the
same stop command for the unreachable case. See the orphaned-box step
in the reset/forget journey.

The grok explore pass's three (2026-07-17, report-only; the
report was absorbed here and deleted) were dispatched the same day:
ca-certificates joined the firewall AND firewall-open skills' apt
lists beside the curl that needed it (codereview caught the sibling;
pinned for both — assertCurlShipsTrustStore in the builtins tests —
and the firewall journey above now asserts HTTPS on the none
template), the
already-configured flag refusal points at `byre config` as the
reconfigure path, and `mcp add --help` carries the argv example
(the word `command` is the TOML key, never part of the argv). The
refusal itself stays — silently ignoring `--agent` on an existing
config would launch the OLD agent under a flag that looked like
consent to the new one.

The same pass confirmed green (no recipes yet — graduate on a future
pass): host mounts + store-edit apt, deliver of a directory, self-edit
round-trip + exit report, skill fork, rehome after `mv`, rebuild,
docker-host containment-hole loudness, forget --force.

The codex lifecycle/config pass also confirmed three-level named-layer
composition and project precedence, live layer-cycle detection/recovery,
self-edit's project-only store mount, invalid-config recovery via
`forget --force`, and canonical identity through a symlinked project path.

Previously: pass-2's six findings were dispatched 2026-07-17 (the PATH
restore, the everywhere-reprompt, the double-[none] guard, the decoded
killed-box exit, and the two worktree labels), each with a regression
test; the recipes above assert the fixed behavior. Future passes append
findings here, never fix mid-pass.

Worth keeping from pass 2's closed threads: reset/forget's
decline-while-running exits 1 (an earlier "rc=0" was a pipe-measurement
artifact — see harness lessons), and the 3-vs-1 asymmetry against
develop's ExitRefused is deliberate (3 exists only because develop's
exit code otherwise carries the agent's own status).
